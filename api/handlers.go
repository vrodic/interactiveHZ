package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"hz-train-map/db"
)

type Broadcaster struct {
	clients map[chan string]bool
	mu      sync.Mutex
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan string]bool),
	}
}

func (b *Broadcaster) Subscribe() chan string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan string, 100)
	b.clients[ch] = true
	return ch
}

func (b *Broadcaster) Unsubscribe(ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.clients[ch]; ok {
		delete(b.clients, ch)
		close(ch)
	}
}

func (b *Broadcaster) Broadcast(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

var (
	zagrebLoc *time.Location
)

func init() {
	loc, err := time.LoadLocation("Europe/Zagreb")
	if err != nil {
		loc = time.FixedZone("CEST", 2*3600)
	}
	zagrebLoc = loc
}

type cachedDelay struct {
	response  DelayAPIResponse
	timestamp time.Time
}

type pendingSpikeRecord struct {
	delayMinutes   int
	lastStation    string
	nextStation    string
	positionStatus string
	timestamp      time.Time
}

type scheduleBounds struct {
	firstDepSec int
	lastArrSec  int
}

type activeTrainsCacheEntry struct {
	data      []byte
	timestamp time.Time
}

type Server struct {
	db                  *sql.DB
	osmGraph            *OSMGraph
	httpClient          *http.Client
	delayCache          sync.Map
	pendingSpikes       sync.Map
	lastQueried         sync.Map
	scheduleBoundsCache sync.Map
	tripTerminalsCache  sync.Map
	stationsMap         sync.Map // stop_id -> Station
	stationsLoaded      bool
	stationsMu          sync.RWMutex
	activeTrainsCacheMu sync.RWMutex
	activeTrainsCache   *activeTrainsCacheEntry
	routeCache          sync.Map
	segmentsCache       []SegmentSpeed
	segmentsJSON        []byte
	segmentsEtag        string
	segmentsMu          sync.RWMutex
	broadcaster         *Broadcaster
	delayStatsMu        sync.Mutex
	delayTotalCalls     int64
	delaySuccessCalls   int64
	delayFailedCalls    int64
	delayLastCallTime   time.Time
	delayCallHistory    []time.Time
}

func NewServer(db *sql.DB) *Server {
	tr := &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Server{
		db:          db,
		httpClient:  &http.Client{Transport: tr, Timeout: 8 * time.Second},
		broadcaster: NewBroadcaster(),
	}
}

func (s *Server) ensureStationsLoaded() {
	s.stationsMu.RLock()
	if s.stationsLoaded {
		s.stationsMu.RUnlock()
		return
	}
	s.stationsMu.RUnlock()

	s.stationsMu.Lock()
	defer s.stationsMu.Unlock()
	if s.stationsLoaded {
		return
	}

	rows, err := s.db.Query("SELECT stop_id, stop_name, stop_lat, stop_lon FROM stations")
	if err != nil {
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var st Station
		if err := rows.Scan(&st.StopID, &st.StopName, &st.Lat, &st.Lon); err == nil {
			s.stationsMap.Store(st.StopID, st)
			count++
		}
	}
	if count > 0 {
		s.stationsLoaded = true
	}
}

func (s *Server) getStation(stopID string) (Station, bool) {
	s.ensureStationsLoaded()
	val, ok := s.stationsMap.Load(stopID)
	if ok {
		return val.(Station), true
	}
	return Station{}, false
}

func (s *Server) invalidateActiveTrainsCache() {
	s.activeTrainsCacheMu.Lock()
	s.activeTrainsCache = nil
	s.activeTrainsCacheMu.Unlock()
}

func (s *Server) recordDelayServiceCall(success bool) {
	s.delayStatsMu.Lock()
	defer s.delayStatsMu.Unlock()

	now := time.Now()
	s.delayTotalCalls++
	if success {
		s.delaySuccessCalls++
	} else {
		s.delayFailedCalls++
	}
	s.delayLastCallTime = now
	s.delayCallHistory = append(s.delayCallHistory, now)

	cutoff := now.Add(-1 * time.Hour)
	idx := 0
	for idx < len(s.delayCallHistory) && s.delayCallHistory[idx].Before(cutoff) {
		idx++
	}
	if idx > 0 {
		s.delayCallHistory = s.delayCallHistory[idx:]
	}
}

func (s *Server) GetDelayServiceStats() DelayServiceStats {
	s.delayStatsMu.Lock()
	defer s.delayStatsMu.Unlock()

	now := time.Now()
	oneMinAgo := now.Add(-1 * time.Minute)
	oneHourAgo := now.Add(-1 * time.Hour)

	callsMin := 0
	callsHour := 0

	for _, t := range s.delayCallHistory {
		if t.After(oneMinAgo) {
			callsMin++
		}
		if t.After(oneHourAgo) {
			callsHour++
		}
	}

	lastCallStr := ""
	if !s.delayLastCallTime.IsZero() {
		lastCallStr = getZagrebTime().Format("15:04:05")
	}

	return DelayServiceStats{
		TotalCalls:      s.delayTotalCalls,
		SuccessfulCalls: s.delaySuccessCalls,
		FailedCalls:     s.delayFailedCalls,
		CallsLastMinute: callsMin,
		CallsLastHour:   callsHour,
		LastCallTime:    lastCallStr,
	}
}

func getZagrebTime() time.Time {
	return time.Now().In(zagrebLoc)
}

func parseTimeParam(tsStr string, defaultSec int) int {
	if tsStr == "" {
		return defaultSec
	}
	if sec, err := strconv.Atoi(tsStr); err == nil {
		return sec
	}
	parts := strings.Split(tsStr, ":")
	if len(parts) >= 2 {
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		sec := 0
		if len(parts) >= 3 {
			sec, _ = strconv.Atoi(parts[2])
		}
		return h*3600 + m*60 + sec
	}
	return defaultSec
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	ps := make([]string, n)
	for i := range ps {
		ps[i] = "?"
	}
	return strings.Join(ps, ",")
}

func toInterfaceSlice(strs []string) []interface{} {
	res := make([]interface{}, len(strs))
	for i, s := range strs {
		res[i] = s
	}
	return res
}

func stripSeconds(timeStr string) string {
	parts := strings.Split(timeStr, ":")
	if len(parts) >= 2 {
		h := parts[0]
		if len(h) == 1 {
			h = "0" + h
		}
		m := parts[1]
		if len(m) == 1 {
			m = "0" + m
		}
		return h + ":" + m
	}
	return timeStr
}

func (s *Server) SetOSMGraph(graph *OSMGraph) {
	s.osmGraph = graph
	s.routeCache = sync.Map{}
	s.scheduleBoundsCache = sync.Map{}
	s.tripTerminalsCache = sync.Map{}
	s.InvalidateSegmentsCache()
}

func (s *Server) LoadOrGenerateSegmentsJSON(segmentsPath string, gtfsChanged bool) ([]SegmentSpeed, error) {
	s.segmentsMu.Lock()
	defer s.segmentsMu.Unlock()

	var rawJSON []byte
	var segments []SegmentSpeed

	if !gtfsChanged && segmentsPath != "" {
		if data, err := os.ReadFile(segmentsPath); err == nil && len(data) > 0 {
			if errUnmarshal := json.Unmarshal(data, &segments); errUnmarshal == nil && len(segments) > 0 {
				rawJSON = data
			}
		}
	}

	if len(rawJSON) == 0 {
		segments = s.ComputeSegments()
		data, err := json.Marshal(segments)
		if err != nil {
			return nil, err
		}
		rawJSON = data

		if segmentsPath != "" {
			_ = os.MkdirAll(filepath.Dir(segmentsPath), 0755)
			_ = os.WriteFile(segmentsPath, rawJSON, 0644)
		}
	}

	s.segmentsCache = segments
	s.segmentsJSON = rawJSON
	s.segmentsEtag = fmt.Sprintf("\"%x\"", sha256.Sum256(rawJSON))

	return segments, nil
}

func (s *Server) GetCachedSegments() []SegmentSpeed {
	s.segmentsMu.RLock()
	if len(s.segmentsCache) > 0 {
		segs := s.segmentsCache
		s.segmentsMu.RUnlock()
		return segs
	}
	s.segmentsMu.RUnlock()

	s.segmentsMu.Lock()
	defer s.segmentsMu.Unlock()
	if len(s.segmentsCache) > 0 {
		return s.segmentsCache
	}
	s.segmentsCache = s.ComputeSegments()
	data, _ := json.Marshal(s.segmentsCache)
	s.segmentsJSON = data
	s.segmentsEtag = fmt.Sprintf("\"%x\"", sha256.Sum256(data))
	return s.segmentsCache
}

func (s *Server) InvalidateSegmentsCache() {
	s.segmentsMu.Lock()
	s.segmentsCache = nil
	s.segmentsJSON = nil
	s.segmentsEtag = ""
	s.segmentsMu.Unlock()

	s.stationsMu.Lock()
	s.stationsLoaded = false
	s.stationsMu.Unlock()
}

func (s *Server) getRouteWaypoints(fromID, toID string, s1Lat, s1Lon, s2Lat, s2Lon float64) []LatLon {
	key := fromID + "->" + toID
	if val, ok := s.routeCache.Load(key); ok {
		return val.([]LatLon)
	}

	if s.osmGraph != nil {
		if osmPath := s.osmGraph.FindShortestPath(s1Lat, s1Lon, s2Lat, s2Lon); len(osmPath) >= 2 {
			s.routeCache.Store(key, osmPath)
			return osmPath
		}
	}

	path := []LatLon{{Lat: s1Lat, Lon: s1Lon}, {Lat: s2Lat, Lon: s2Lon}}
	s.routeCache.Store(key, path)
	return path
}

func interpolateAlongPath(path []LatLon, progress float64) (float64, float64) {
	if len(path) == 0 {
		return 0, 0
	}
	if len(path) == 1 || progress <= 0 {
		return path[0].Lat, path[0].Lon
	}
	if progress >= 1 {
		return path[len(path)-1].Lat, path[len(path)-1].Lon
	}

	type segLen struct {
		length float64
		p1, p2 LatLon
	}
	var segs []segLen
	totalLen := 0.0

	for i := 0; i < len(path)-1; i++ {
		p1 := path[i]
		p2 := path[i+1]
		d := haversineKm(p1.Lat, p1.Lon, p2.Lat, p2.Lon)
		if d < 1e-6 {
			d = 1e-6
		}
		segs = append(segs, segLen{length: d, p1: p1, p2: p2})
		totalLen += d
	}

	if totalLen <= 0 {
		return path[0].Lat, path[0].Lon
	}

	targetDist := progress * totalLen
	accum := 0.0

	for _, seg := range segs {
		if accum+seg.length >= targetDist {
			segProgress := (targetDist - accum) / seg.length
			if segProgress < 0 {
				segProgress = 0
			}
			if segProgress > 1 {
				segProgress = 1
			}
			lat := seg.p1.Lat + (seg.p2.Lat-seg.p1.Lat)*segProgress
			lon := seg.p1.Lon + (seg.p2.Lon-seg.p1.Lon)*segProgress
			return lat, lon
		}
		accum += seg.length
	}

	return path[len(path)-1].Lat, path[len(path)-1].Lon
}

func (s *Server) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]SearchResultItem{})
		return
	}

	likeQ := "%" + q + "%"
	results := make([]SearchResultItem, 0)

	stRows, err := s.db.Query(`
		SELECT stop_id, stop_name, stop_lat, stop_lon
		FROM stations
		WHERE stop_name LIKE ?
		ORDER BY CASE WHEN stop_name LIKE ? THEN 0 ELSE 1 END, stop_name ASC
		LIMIT 10
	`, likeQ, q+"%")
	if err == nil {
		for stRows.Next() {
			var st Station
			if err := stRows.Scan(&st.StopID, &st.StopName, &st.Lat, &st.Lon); err == nil {
				results = append(results, SearchResultItem{
					Type:     "station",
					ID:       st.StopID,
					Title:    st.StopName,
					Subtitle: "Station",
					Lat:      st.Lat,
					Lon:      st.Lon,
				})
			}
		}
		stRows.Close()
	}

	trRows, err := s.db.Query(`
		SELECT COALESCE(t.trip_short_name, t.trip_id) as train_num, COALESCE(t.trip_headsign, ''),
		       s.stop_lat, s.stop_lon
		FROM trips t
		JOIN stop_times st ON t.trip_id = st.trip_id AND st.stop_sequence = 1
		JOIN stations s ON st.stop_id = s.stop_id
		WHERE t.trip_short_name LIKE ? OR t.trip_id LIKE ? OR t.trip_headsign LIKE ?
		GROUP BY train_num
		ORDER BY CASE WHEN t.trip_short_name = ? THEN 0 ELSE 1 END, train_num ASC
		LIMIT 10
	`, likeQ, likeQ, likeQ, q)
	if err == nil {
		seenTrains := make(map[string]bool)
		for trRows.Next() {
			var trNum, headsign string
			var lat, lon float64
			if err := trRows.Scan(&trNum, &headsign, &lat, &lon); err == nil {
				if seenTrains[trNum] {
					continue
				}
				seenTrains[trNum] = true
				sub := "Train"
				if headsign != "" {
					sub = "Train - " + headsign
				}
				results = append(results, SearchResultItem{
					Type:        "train",
					ID:          trNum,
					Title:       "Train " + trNum,
					Subtitle:    sub,
					Lat:         lat,
					Lon:         lon,
					TrainNumber: trNum,
				})
			}
		}
		trRows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) GetNotices(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`
		SELECT id, category, title, content, parsed_stations, parsed_trains, pdf_urls,
		       is_bus_replacement, is_track_works, COALESCE(date_from, ''), COALESCE(date_to, ''),
		       COALESCE(dates_json, '[]'), updated_at
		FROM traffic_notices
		ORDER BY is_track_works DESC, is_bus_replacement DESC, id ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	notices := make([]NoticeResponse, 0)
	for rows.Next() {
		var n NoticeResponse
		var stJSON, trJSON, pdfJSON, datesJSONStr string
		var isBus, isWorks int

		if err := rows.Scan(&n.ID, &n.Category, &n.Title, &n.Content, &stJSON, &trJSON, &pdfJSON, &isBus, &isWorks, &n.DateFrom, &n.DateTo, &datesJSONStr, &n.UpdatedAt); err != nil {
			continue
		}

		_ = json.Unmarshal([]byte(stJSON), &n.ParsedStations)
		_ = json.Unmarshal([]byte(trJSON), &n.ParsedTrains)
		_ = json.Unmarshal([]byte(pdfJSON), &n.PDFURLs)
		_ = json.Unmarshal([]byte(datesJSONStr), &n.DatesJSON)

		n.IsBusReplacement = isBus == 1
		n.IsTrackWorks = isWorks == 1

		notices = append(notices, n)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notices)
}

func (s *Server) fetchNoticesFromDB() []NoticeResponse {
	rows, err := s.db.Query(`
		SELECT id, category, title, content, parsed_stations, parsed_trains, pdf_urls,
		       is_bus_replacement, is_track_works, COALESCE(date_from, ''), COALESCE(date_to, ''),
		       COALESCE(dates_json, '[]'), updated_at
		FROM traffic_notices
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var notices []NoticeResponse
	for rows.Next() {
		var n NoticeResponse
		var stJSON, trJSON, pdfJSON, datesJSONStr string
		var isBus, isWorks int

		if err := rows.Scan(&n.ID, &n.Category, &n.Title, &n.Content, &stJSON, &trJSON, &pdfJSON, &isBus, &isWorks, &n.DateFrom, &n.DateTo, &datesJSONStr, &n.UpdatedAt); err == nil {
			_ = json.Unmarshal([]byte(stJSON), &n.ParsedStations)
			_ = json.Unmarshal([]byte(trJSON), &n.ParsedTrains)
			_ = json.Unmarshal([]byte(pdfJSON), &n.PDFURLs)
			_ = json.Unmarshal([]byte(datesJSONStr), &n.DatesJSON)
			n.IsBusReplacement = isBus == 1
			n.IsTrackWorks = isWorks == 1
			notices = append(notices, n)
		}
	}
	return notices
}

func isNoticeActiveOnDate(n NoticeResponse, dateStr string) bool {
	if len(n.DatesJSON) > 0 {
		for _, d := range n.DatesJSON {
			if d == dateStr {
				return true
			}
		}
		return false
	}
	if n.DateFrom != "" && dateStr < n.DateFrom {
		return false
	}
	if n.DateTo != "" && dateStr > n.DateTo {
		return false
	}
	return true
}

func (s *Server) getTripStationNames(tripID string) []string {
	if tripID == "" {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT DISTINCT s.stop_name
		FROM stop_times st
		JOIN stations s ON st.stop_id = s.stop_id
		WHERE st.trip_id = ?
	`, tripID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			names = append(names, name)
		}
	}
	return names
}

func normalizeStationName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "č", "c")
	s = strings.ReplaceAll(s, "ć", "c")
	s = strings.ReplaceAll(s, "đ", "d")
	s = strings.ReplaceAll(s, "š", "s")
	s = strings.ReplaceAll(s, "ž", "z")
	return strings.Join(strings.Fields(s), " ")
}

func matchStationNames(st1, st2 string) bool {
	if st1 == "" || st2 == "" {
		return false
	}
	if strings.EqualFold(st1, st2) {
		return true
	}
	s1 := normalizeStationName(st1)
	s2 := normalizeStationName(st2)
	return s1 == s2 && s1 != ""
}

func (s *Server) getIntermediateStations(stationA, stationB string) []string {
	if stationA == "" || stationB == "" {
		return nil
	}
	normA := normalizeStationName(stationA)
	normB := normalizeStationName(stationB)
	if normA == "" || normB == "" || normA == normB {
		return []string{stationA}
	}

	if s != nil {
		cacheKey := "inter_st:" + normA + "||" + normB
		if val, ok := s.routeCache.Load(cacheKey); ok {
			return val.([]string)
		}
	}

	if s == nil || s.db == nil {
		return []string{stationA, stationB}
	}

	// 1. Find stop_ids for stationA and stationB using in-memory station map / matchStationNames
	var stopIDsA, stopIDsB []string
	if s != nil {
		s.ensureStationsLoaded()
		s.stationsMap.Range(func(key, value interface{}) bool {
			st := value.(Station)
			if matchStationNames(st.StopName, stationA) {
				stopIDsA = append(stopIDsA, st.StopID)
			}
			if matchStationNames(st.StopName, stationB) {
				stopIDsB = append(stopIDsB, st.StopID)
			}
			return true
		})
	}

	if (len(stopIDsA) == 0 || len(stopIDsB) == 0) && s != nil && s.db != nil {
		rowsA, errA := s.db.Query("SELECT stop_id, stop_name FROM stations")
		if errA == nil {
			for rowsA.Next() {
				var id, name string
				if err := rowsA.Scan(&id, &name); err == nil {
					if matchStationNames(name, stationA) {
						stopIDsA = append(stopIDsA, id)
					}
					if matchStationNames(name, stationB) {
						stopIDsB = append(stopIDsB, id)
					}
				}
			}
			rowsA.Close()
		}
	}

	if len(stopIDsA) == 0 || len(stopIDsB) == 0 {
		return []string{stationA, stationB}
	}

	// 2. Query stop_times with indexed stop_id lookups
	tripMapA := make(map[string]int)
	placeholdersA := makePlaceholders(len(stopIDsA))
	argsA := toInterfaceSlice(stopIDsA)
	rowsTripsA, errTripsA := s.db.Query("SELECT trip_id, stop_sequence FROM stop_times WHERE stop_id IN ("+placeholdersA+")", argsA...)
	if errTripsA == nil {
		for rowsTripsA.Next() {
			var tid string
			var seq int
			if err := rowsTripsA.Scan(&tid, &seq); err == nil {
				tripMapA[tid] = seq
			}
		}
		rowsTripsA.Close()
	}

	var targetTripID string
	var minSeq, maxSeq int
	placeholdersB := makePlaceholders(len(stopIDsB))
	argsB := toInterfaceSlice(stopIDsB)
	rowsTripsB, errTripsB := s.db.Query("SELECT trip_id, stop_sequence FROM stop_times WHERE stop_id IN ("+placeholdersB+")", argsB...)
	if errTripsB == nil {
		for rowsTripsB.Next() {
			var tid string
			var seqB int
			if err := rowsTripsB.Scan(&tid, &seqB); err == nil {
				if seqA, found := tripMapA[tid]; found {
					targetTripID = tid
					if seqA < seqB {
						minSeq, maxSeq = seqA, seqB
					} else {
						minSeq, maxSeq = seqB, seqA
					}
					break
				}
			}
		}
		rowsTripsB.Close()
	}

	if targetTripID == "" {
		return []string{stationA, stationB}
	}

	// 3. Retrieve intermediate stop names using indexed trip_id + stop_sequence
	rowsStops, errStops := s.db.Query(`
		SELECT DISTINCT s.stop_name
		FROM stop_times st
		JOIN stations s ON st.stop_id = s.stop_id
		WHERE st.trip_id = ? AND st.stop_sequence >= ? AND st.stop_sequence <= ?
		ORDER BY st.stop_sequence ASC
	`, targetTripID, minSeq, maxSeq)

	var stations []string
	if errStops == nil {
		for rowsStops.Next() {
			var name string
			if err := rowsStops.Scan(&name); err == nil {
				stations = append(stations, name)
			}
		}
		rowsStops.Close()
	}

	if len(stations) == 0 {
		stations = []string{stationA, stationB}
	}

	if s != nil {
		cacheKey := "inter_st:" + normA + "||" + normB
		s.routeCache.Store(cacheKey, stations)
	}
	return stations
}

func (s *Server) getNoticeExpandedStations(n NoticeResponse) (fullSet []string, busSet []string) {
	if len(n.ParsedStations) == 0 {
		return nil, nil
	}

	if s != nil {
		cacheKey := "notice_exp:" + n.ID
		if val, ok := s.routeCache.Load(cacheKey); ok {
			pair := val.([2][]string)
			return pair[0], pair[1]
		}
	}

	fullMap := make(map[string]bool)
	for _, st := range n.ParsedStations {
		fullMap[st] = true
	}

	for i := 0; i < len(n.ParsedStations); i++ {
		for j := i + 1; j < len(n.ParsedStations); j++ {
			stA := n.ParsedStations[i]
			stB := n.ParsedStations[j]
			intermediates := s.getIntermediateStations(stA, stB)
			for _, inter := range intermediates {
				fullMap[inter] = true
			}
		}
	}

	fullSet = make([]string, 0, len(fullMap))
	for st := range fullMap {
		fullSet = append(fullSet, st)
	}

	if n.IsBusReplacement {
		busMap := make(map[string]bool)
		text := strings.ToLower(n.Title + " \n " + n.Content)

		// Split text into clauses/sentences to match stations with local closure keywords
		clauses := strings.FieldsFunc(text, func(r rune) bool {
			return r == '.' || r == ',' || r == ';' || r == '\n' || r == '\r'
		})

		var busPairs [][2]string
		for _, clause := range clauses {
			normClause := normalizeStationName(clause)
			if strings.Contains(clause, "zatvoren") || strings.Contains(clause, "autobus") || strings.Contains(clause, "zamjensk") || strings.Contains(clause, " bus") {
				for i := 0; i < len(n.ParsedStations); i++ {
					for j := i + 1; j < len(n.ParsedStations); j++ {
						stA := n.ParsedStations[i]
						stB := n.ParsedStations[j]
						normA := normalizeStationName(stA)
						normB := normalizeStationName(stB)

						if strings.Contains(normClause, normA) && strings.Contains(normClause, normB) {
							busPairs = append(busPairs, [2]string{stA, stB})
						}
					}
				}
			}
		}

		if len(busPairs) > 0 {
			for _, pair := range busPairs {
				intermediates := s.getIntermediateStations(pair[0], pair[1])
				for _, inter := range intermediates {
					busMap[inter] = true
				}
			}
			busSet = make([]string, 0, len(busMap))
			for st := range busMap {
				busSet = append(busSet, st)
			}
		} else {
			busSet = fullSet
		}
	}

	if s != nil {
		cacheKey := "notice_exp:" + n.ID
		s.routeCache.Store(cacheKey, [2][]string{fullSet, busSet})
	}
	return fullSet, busSet
}

func isNoticeApplicable(n NoticeResponse, trainNum string, stationNames []string, dateStr string) (matches bool, isBus bool, isWorks bool, noticeID string) {
	var s *Server
	return s.isNoticeApplicable(n, trainNum, stationNames, dateStr)
}

func (s *Server) isNoticeApplicable(n NoticeResponse, trainNum string, stationNames []string, dateStr string) (matches bool, isBus bool, isWorks bool, noticeID string) {
	if !isNoticeActiveOnDate(n, dateStr) {
		return false, false, false, ""
	}

	// 1. If notice explicitly lists train numbers, match ONLY if this train's number is listed
	if len(n.ParsedTrains) > 0 {
		trMatched := false
		for _, tr := range n.ParsedTrains {
			if tr == trainNum {
				trMatched = true
				break
			}
		}
		if !trMatched {
			return false, false, false, ""
		}
		return true, n.IsBusReplacement, n.IsTrackWorks, n.ID
	}

	// 2. Otherwise match based on affected station overlap
	if len(n.ParsedStations) > 0 {
		fullSet, busSet := s.getNoticeExpandedStations(n)

		matchedCount := 0
		for _, st := range fullSet {
			for _, stName := range stationNames {
				if matchStationNames(st, stName) {
					matchedCount++
					break
				}
			}
		}

		required := 2
		if len(n.ParsedStations) == 1 {
			required = 1
		}

		if matchedCount < required {
			return false, false, false, ""
		}

		isBus = false
		if n.IsBusReplacement && len(busSet) > 0 {
			busMatchedCount := 0
			for _, st := range busSet {
				for _, stName := range stationNames {
					if matchStationNames(st, stName) {
						busMatchedCount++
						break
					}
				}
			}
			busRequired := 2
			if len(busSet) == 1 {
				busRequired = 1
			}
			if busMatchedCount >= busRequired {
				isBus = true
			}
		}

		isWorks = n.IsTrackWorks
		return true, isBus, isWorks, n.ID
	}

	return false, false, false, ""
}

func (s *Server) GetStations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`
		SELECT stop_id, stop_name, stop_lat, stop_lon
		FROM stations
		ORDER BY stop_name ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	stations := make([]Station, 0)
	for rows.Next() {
		var st Station
		if err := rows.Scan(&st.StopID, &st.StopName, &st.Lat, &st.Lon); err != nil {
			continue
		}
		stations = append(stations, st)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stations)
}

func getCalendarCondition(t time.Time) (string, []interface{}) {
	dateStr := t.Format("20060102")
	weekday := strings.ToLower(t.Weekday().String())

	cond := fmt.Sprintf(`(
		t.service_id IN (SELECT service_id FROM calendar_dates WHERE date = ? AND exception_type = 1)
		OR (
			t.service_id NOT IN (SELECT service_id FROM calendar_dates WHERE date = ? AND exception_type = 2)
			AND (c.service_id IS NULL OR (c.start_date <= ? AND c.end_date >= ? AND c.%s = 1))
		)
	)`, weekday)

	return cond, []interface{}{dateStr, dateStr, dateStr, dateStr}
}

func (s *Server) getTrainDelayMinutes(trainNum string) (int, bool) {
	var delayMins int
	err := s.db.QueryRow(`
		SELECT delay_minutes
		FROM train_delays
		WHERE train_number = ?
	`, trainNum).Scan(&delayMins)
	if err == nil {
		return delayMins, true
	}

	errHist := s.db.QueryRow(`
		SELECT delay_minutes
		FROM historical_train_delays
		WHERE train_number = ?
		ORDER BY id DESC LIMIT 1
	`, trainNum).Scan(&delayMins)
	if errHist == nil {
		return delayMins, true
	}

	return 0, false
}

func (s *Server) GetStationTimetable(w http.ResponseWriter, r *http.Request) {
	stopID := strings.TrimPrefix(r.URL.Path, "/api/stations/")
	stopID = strings.TrimSuffix(stopID, "/timetable")

	if stopID == "" {
		http.Error(w, "Station ID required", http.StatusBadRequest)
		return
	}

	zgNow := getZagrebTime()
	calCond, calArgs := getCalendarCondition(zgNow)

	query := fmt.Sprintf(`
		SELECT MIN(st.trip_id), COALESCE(t.trip_short_name, t.trip_id) AS train_num, COALESCE(t.trip_headsign, ''),
		       COALESCE(last_s.stop_name, COALESCE(t.trip_headsign, '')),
		       st.arrival_time, st.departure_time, MIN(st.stop_sequence)
		FROM stop_times st
		JOIN trips t ON st.trip_id = t.trip_id
		LEFT JOIN calendar c ON t.service_id = c.service_id
		LEFT JOIN stop_times st_last ON st_last.trip_id = t.trip_id AND st_last.stop_sequence = (
			SELECT MAX(stop_sequence) FROM stop_times WHERE trip_id = t.trip_id
		)
		LEFT JOIN stations last_s ON st_last.stop_id = last_s.stop_id
		WHERE st.stop_id = ? AND %s
		GROUP BY train_num, st.departure_time, st.arrival_time
		ORDER BY st.departure_seconds ASC
		LIMIT 200
	`, calCond)

	args := append([]interface{}{stopID}, calArgs...)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	allNotices := s.fetchNoticesFromDB()
	var stationName string
	_ = s.db.QueryRow("SELECT stop_name FROM stations WHERE stop_id = ?", stopID).Scan(&stationName)

	entries := make([]StationTimetableEntry, 0)
	for rows.Next() {
		var entry StationTimetableEntry
		if err := rows.Scan(&entry.TripID, &entry.TrainNumber, &entry.Headsign, &entry.DestinationStationName, &entry.ArrivalTime, &entry.DepartureTime, &entry.StopSequence); err != nil {
			continue
		}
		if entry.DestinationStationName == "" {
			entry.DestinationStationName = entry.Headsign
		}

		var delayMins int
		cutoffStr := zgNow.Add(-6 * time.Hour).Format("2006-01-02 15:04:05")
		err := s.db.QueryRow("SELECT delay_minutes FROM train_delays WHERE train_number = ? AND updated_at >= ?", entry.TrainNumber, cutoffStr).Scan(&delayMins)
		if err == nil {
			entry.DelayMinutes = &delayMins
		}

		todayStr := zgNow.Format("2006-01-02")
		stNames := s.getTripStationNames(entry.TripID)
		if len(stNames) == 0 {
			stNames = []string{stationName, entry.DestinationStationName}
		}
		for _, n := range allNotices {
			matched, isBus, isWorks, noticeID := s.isNoticeApplicable(n, entry.TrainNumber, stNames, todayStr)
			if matched {
				if isBus {
					entry.IsBusReplacement = true
				}
				if isWorks {
					entry.IsTrackWorks = true
				}
				if entry.NoticeID == "" {
					entry.NoticeID = noticeID
				}
			}
		}

		entries = append(entries, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (s *Server) getTripTerminals(tripID string) (firstStation, lastStation string) {
	if val, ok := s.tripTerminalsCache.Load(tripID); ok {
		terms := val.([2]string)
		return terms[0], terms[1]
	}

	var firstID, lastID string
	err := s.db.QueryRow(`
		SELECT
			(SELECT stop_id FROM stop_times WHERE trip_id = ? ORDER BY stop_sequence ASC LIMIT 1),
			(SELECT stop_id FROM stop_times WHERE trip_id = ? ORDER BY stop_sequence DESC LIMIT 1)
	`, tripID, tripID).Scan(&firstID, &lastID)

	var firstName, lastName string
	if err == nil {
		if st, ok := s.getStation(firstID); ok {
			firstName = st.StopName
		}
		if st, ok := s.getStation(lastID); ok {
			lastName = st.StopName
		}
	}

	terms := [2]string{firstName, lastName}
	s.tripTerminalsCache.Store(tripID, terms)
	return firstName, lastName
}

func (s *Server) ComputeActiveTrains(secondsOfDay int) ([]ActiveTrain, error) {
	s.ensureStationsLoaded()
	zgNow := getZagrebTime()

	delayMap := make(map[string]int)
	cutoffStr := zgNow.Add(-6 * time.Hour).Format("2006-01-02 15:04:05")
	if delayRows, err := s.db.Query("SELECT train_number, delay_minutes FROM train_delays WHERE updated_at >= ?", cutoffStr); err == nil {
		defer delayRows.Close()
		for delayRows.Next() {
			var trNum string
			var delMins int
			if err := delayRows.Scan(&trNum, &delMins); err == nil {
				delayMap[trNum] = delMins
			}
		}
	}

	windowStart := secondsOfDay - 10800
	windowEnd := secondsOfDay + 1800

	calCond, calArgs := getCalendarCondition(zgNow)

	calCondInTrips := strings.ReplaceAll(calCond, "t.service_id", "t2.service_id")

	query := fmt.Sprintf(`
		SELECT t.trip_id, COALESCE(t.trip_short_name, t.trip_id), COALESCE(t.trip_headsign, ''),
		       st1.stop_id, st1.departure_seconds,
		       st2.stop_id, st2.arrival_seconds
		FROM trips t
		JOIN stop_times st1 ON st1.trip_id = t.trip_id
		JOIN stop_times st2 ON st2.trip_id = t.trip_id AND st2.stop_sequence = st1.stop_sequence + 1
		WHERE t.trip_id IN (
			SELECT t2.trip_id FROM trips t2
			LEFT JOIN calendar c ON t2.service_id = c.service_id
			WHERE %s
		)
		AND st1.departure_seconds <= ? AND st2.arrival_seconds >= ?
	`, calCondInTrips)

	args := append(calArgs, windowEnd, windowStart)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		fmt.Printf("ComputeActiveTrains DB Query Error: %v\n", err)
		return nil, err
	}
	defer rows.Close()

	activeTrainsMap := make(map[string]ActiveTrain)

	for rows.Next() {
		var (
			tripID, trainNum, headsign string
			s1ID                       string
			s1DepSec                   int
			s2ID                       string
			s2ArrSec                   int
		)

		if err := rows.Scan(&tripID, &trainNum, &headsign, &s1ID, &s1DepSec, &s2ID, &s2ArrSec); err != nil {
			continue
		}

		s1, ok1 := s.getStation(s1ID)
		s2, ok2 := s.getStation(s2ID)
		if !ok1 || !ok2 {
			continue
		}

		delayMinutes := delayMap[trainNum]
		effectiveTime := secondsOfDay - (delayMinutes * 60)

		if effectiveTime >= s1DepSec && effectiveTime <= s2ArrSec {
			duration := float64(s2ArrSec - s1DepSec)
			progress := 0.0
			status := "running"

			if duration > 0 {
				progress = float64(effectiveTime-s1DepSec) / duration
				if progress < 0 {
					progress = 0
				}
				if progress > 1 {
					progress = 1
				}
			} else {
				status = "at_station"
			}

			pathWaypoints := s.getRouteWaypoints(s1ID, s2ID, s1.Lat, s1.Lon, s2.Lat, s2.Lon)
			curLat, curLon := interpolateAlongPath(pathWaypoints, progress)

			if existing, ok := activeTrainsMap[trainNum]; !ok || (progress > 0 && progress < 1 && (existing.Progress <= 0 || existing.Progress >= 1)) {
				firstSt, lastSt := s.getTripTerminals(tripID)
				if firstSt == "" {
					firstSt = headsign
				}
				if lastSt == "" {
					lastSt = headsign
				}

				var fDepTime, lArrTime string
				_ = s.db.QueryRow(`
					SELECT st_first.departure_time, st_last.arrival_time
					FROM stop_times st_first
					JOIN stop_times st_last ON st_last.trip_id = st_first.trip_id AND st_last.stop_sequence = (
						SELECT MAX(stop_sequence) FROM stop_times WHERE trip_id = st_first.trip_id
					)
					WHERE st_first.trip_id = ? AND st_first.stop_sequence = 1
				`, tripID).Scan(&fDepTime, &lArrTime)

				activeTrainsMap[trainNum] = ActiveTrain{
					TripID:             tripID,
					TrainNumber:        trainNum,
					Headsign:           headsign,
					CurrentLat:         curLat,
					CurrentLon:         curLon,
					PrevStationID:      s1ID,
					PrevStationName:    s1.StopName,
					NextStationID:      s2ID,
					NextStationName:    s2.StopName,
					FirstStationName:   firstSt,
					LastStationName:    lastSt,
					FirstDepartureTime: fDepTime,
					LastArrivalTime:    lArrTime,
					SegmentID:          s1ID + "->" + s2ID,
					Progress:           progress,
					Status:             status,
					DelayMinutes:       delayMinutes,
					ScheduledDepSec:    s1DepSec,
					ScheduledArrSec:    s2ArrSec,
				}
			}
		}
	}

	allNotices := s.fetchNoticesFromDB()
	todayStr := zgNow.Format("2006-01-02")

	activeTrains := make([]ActiveTrain, 0, len(activeTrainsMap))
	for _, train := range activeTrainsMap {
		stNames := s.getTripStationNames(train.TripID)
		if len(stNames) == 0 {
			stNames = []string{train.PrevStationName, train.NextStationName, train.FirstStationName, train.LastStationName}
		}
		for _, n := range allNotices {
			matched, isBus, isWorks, noticeID := s.isNoticeApplicable(n, train.TrainNumber, stNames, todayStr)
			if matched {
				if isBus {
					train.IsBusReplacement = true
				}
				if isWorks {
					train.IsTrackWorks = true
				}
				if train.NoticeID == "" {
					train.NoticeID = noticeID
				}
			}
		}
		activeTrains = append(activeTrains, train)
	}

	return activeTrains, nil
}

func (s *Server) GetTrainDetails(w http.ResponseWriter, r *http.Request) {
	s.ensureStationsLoaded()
	trainNum := strings.TrimPrefix(r.URL.Path, "/api/trains/")
	trainNum = strings.TrimSpace(trainNum)
	if trainNum == "" {
		trainNum = strings.TrimSpace(r.URL.Query().Get("train_number"))
	}
	if trainNum == "" {
		http.Error(w, "train_number parameter is required", http.StatusBadRequest)
		return
	}

	// Fetch live delay if delay hasn't been updated in the last 30 seconds
	shouldFetch := true
	if val, ok := s.lastQueried.Load(trainNum); ok {
		if lastTime, ok := val.(time.Time); ok && time.Since(lastTime) < 30*time.Second {
			shouldFetch = false
		}
	}
	if shouldFetch {
		_, _ = s.FetchAndSaveTrainDelay(trainNum)
	}

	zgNow := getZagrebTime()
	nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()

	var details TrainDetails
	details.TrainNumber = trainNum

	calCond, calArgs := getCalendarCondition(zgNow)

	query := fmt.Sprintf(`
		SELECT t.trip_id, COALESCE(t.trip_headsign, ''),
		       s_first.stop_name, st_first.departure_time, st_first.departure_seconds,
		       s_last.stop_name, st_last.arrival_time, st_last.arrival_seconds
		FROM trips t
		LEFT JOIN calendar c ON t.service_id = c.service_id
		JOIN stop_times st_first ON st_first.trip_id = t.trip_id AND st_first.stop_sequence = 1
		JOIN stations s_first ON st_first.stop_id = s_first.stop_id
		JOIN stop_times st_last ON st_last.trip_id = t.trip_id AND st_last.stop_sequence = (
			SELECT MAX(stop_sequence) FROM stop_times WHERE trip_id = t.trip_id
		)
		JOIN stations s_last ON st_last.stop_id = s_last.stop_id
		WHERE (t.trip_short_name = ? OR t.trip_id = ?) AND %s
		ORDER BY (
			SELECT COUNT(*) FROM stop_times WHERE trip_id = t.trip_id
		) DESC
		LIMIT 1
	`, calCond)

	queryArgs := []interface{}{trainNum, trainNum}
	queryArgs = append(queryArgs, calArgs...)

	err := s.db.QueryRow(query, queryArgs...).Scan(
		&details.TripID, &details.Headsign,
		&details.FirstStationName, &details.FirstDepartureTime, &details.FirstDepartureSeconds,
		&details.LastStationName, &details.LastArrivalTime, &details.LastArrivalSeconds,
	)
	if err != nil {
		// Fallback query if no active trip matches calendar for today
		fallbackQuery := `
			SELECT t.trip_id, COALESCE(t.trip_headsign, ''),
			       s_first.stop_name, st_first.departure_time, st_first.departure_seconds,
			       s_last.stop_name, st_last.arrival_time, st_last.arrival_seconds
			FROM trips t
			JOIN stop_times st_first ON st_first.trip_id = t.trip_id AND st_first.stop_sequence = 1
			JOIN stations s_first ON st_first.stop_id = s_first.stop_id
			JOIN stop_times st_last ON st_last.trip_id = t.trip_id AND st_last.stop_sequence = (
				SELECT MAX(stop_sequence) FROM stop_times WHERE trip_id = t.trip_id
			)
			JOIN stations s_last ON st_last.stop_id = s_last.stop_id
			WHERE t.trip_short_name = ? OR t.trip_id = ?
			ORDER BY (
				SELECT COUNT(*) FROM stop_times WHERE trip_id = t.trip_id
			) DESC
			LIMIT 1
		`
		err = s.db.QueryRow(fallbackQuery, trainNum, trainNum).Scan(
			&details.TripID, &details.Headsign,
			&details.FirstStationName, &details.FirstDepartureTime, &details.FirstDepartureSeconds,
			&details.LastStationName, &details.LastArrivalTime, &details.LastArrivalSeconds,
		)
	}
	if err != nil {
		http.Error(w, "Train not found", http.StatusNotFound)
		return
	}

	if details.FirstStationName == "" {
		details.FirstStationName = details.Headsign
	}
	if details.LastStationName == "" {
		details.LastStationName = details.Headsign
	}

	var delayMins int
	var posStatus string
	_ = s.db.QueryRow("SELECT delay_minutes, COALESCE(position_status, '') FROM train_delays WHERE train_number = ?", trainNum).Scan(&delayMins, &posStatus)
	details.DelayMinutes = delayMins
	details.PositionStatus = posStatus

	// Targeted calculation for single train active status & coordinates using details.TripID directly
	effectiveTime := nowSec - (delayMins * 60)
	activeQuery := `
		SELECT st1.stop_id, st1.departure_seconds,
		       st2.stop_id, st2.arrival_seconds
		FROM stop_times st1
		JOIN stop_times st2 ON st1.trip_id = st2.trip_id AND st2.stop_sequence = st1.stop_sequence + 1
		WHERE st1.trip_id = ?
		  AND st1.departure_seconds <= ? AND st2.arrival_seconds >= ?
		LIMIT 1
	`

	var (
		s1ID, s2ID       string
		s1DepSec, s2ArrSec int
	)
	if err := s.db.QueryRow(activeQuery, details.TripID, effectiveTime, effectiveTime).Scan(&s1ID, &s1DepSec, &s2ID, &s2ArrSec); err == nil {
		s1, ok1 := s.getStation(s1ID)
		s2, ok2 := s.getStation(s2ID)
		if ok1 && ok2 {
			details.IsActive = true
			duration := float64(s2ArrSec - s1DepSec)
			progress := 0.0
			if duration > 0 {
				progress = float64(effectiveTime-s1DepSec) / duration
				if progress < 0 {
					progress = 0
				}
				if progress > 1 {
					progress = 1
				}
			}
			pathWaypoints := s.getRouteWaypoints(s1ID, s2ID, s1.Lat, s1.Lon, s2.Lat, s2.Lon)
			details.Lat, details.Lon = interpolateAlongPath(pathWaypoints, progress)
		}
	}

	details.DelayDistribution = s.computeDelayDistribution(trainNum)
	details.RecentDelays = s.getRecentDelays(trainNum)

	allNotices := s.fetchNoticesFromDB()
	todayStr := zgNow.Format("2006-01-02")
	stNames := s.getTripStationNames(details.TripID)
	if len(stNames) == 0 {
		stNames = []string{details.FirstStationName, details.LastStationName}
	}

	var trainNotices []NoticeResponse
	for _, n := range allNotices {
		matched, isBus, isWorks, noticeID := s.isNoticeApplicable(n, trainNum, stNames, todayStr)
		if matched {
			if isBus {
				details.IsBusReplacement = true
			}
			if isWorks {
				details.IsTrackWorks = true
			}
			if details.NoticeID == "" {
				details.NoticeID = noticeID
			}
			trainNotices = append(trainNotices, n)
		}
	}
	details.Notices = trainNotices

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

func (s *Server) getRecentDelays(trainNum string) []RecentDelayLog {
	cutoffDate := getZagrebTime().AddDate(0, 0, -3).Format("2006-01-02")
	rows, err := s.db.Query(`
		SELECT delay_date, delay_minutes, COALESCE(position_status, ''), COALESCE(last_station, ''), updated_at
		FROM historical_train_delays
		WHERE train_number = ? AND (delay_date >= ? OR updated_at >= ?)
		ORDER BY updated_at DESC, id DESC
	`, trainNum, cutoffDate, cutoffDate)
	if err != nil {
		return []RecentDelayLog{}
	}
	defer rows.Close()

	logs := make([]RecentDelayLog, 0)
	loc := zagrebLoc

	for rows.Next() {
		var l RecentDelayLog
		var rawUpdatedAt string
		if err := rows.Scan(&l.DelayDate, &l.DelayMinutes, &l.PositionStatus, &l.LastStation, &rawUpdatedAt); err == nil {
			var parsedTime time.Time
			var parseErr error
			formats := []string{
				"2006-01-02 15:04:05",
				time.RFC3339,
				"2006-01-02T15:04:05Z",
				"2006-01-02T15:04:05",
			}
			for _, f := range formats {
				parsedTime, parseErr = time.ParseInLocation(f, rawUpdatedAt, loc)
				if parseErr == nil {
					break
				}
			}
			if parseErr == nil {
				l.UpdatedAt = parsedTime.Format("15:04")
			} else {
				parts := strings.Split(rawUpdatedAt, " ")
				if len(parts) > 1 {
					timeParts := strings.Split(parts[1], ":")
					if len(timeParts) >= 2 {
						l.UpdatedAt = timeParts[0] + ":" + timeParts[1]
					} else {
						l.UpdatedAt = rawUpdatedAt
					}
				} else if strings.Contains(rawUpdatedAt, "T") {
					tParts := strings.Split(rawUpdatedAt, "T")
					if len(tParts) > 1 {
						timeParts := strings.Split(tParts[1], ":")
						if len(timeParts) >= 2 {
							l.UpdatedAt = timeParts[0] + ":" + timeParts[1]
						} else {
							l.UpdatedAt = rawUpdatedAt
						}
					} else {
						l.UpdatedAt = rawUpdatedAt
					}
				} else {
					l.UpdatedAt = rawUpdatedAt
				}
			}
			logs = append(logs, l)
		}
	}
	return logs
}

func (s *Server) computeDelayDistribution(trainNum string) *DelayDistribution {
	var total, onTime, late1To5, late5To10, late10To30, lateOver30 int
	err := s.db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN delay_minutes <= 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN delay_minutes > 0 AND delay_minutes <= 5 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN delay_minutes > 5 AND delay_minutes <= 10 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN delay_minutes > 10 AND delay_minutes <= 30 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN delay_minutes > 30 THEN 1 ELSE 0 END), 0)
		FROM historical_train_delays
		WHERE train_number = ?
	`, trainNum).Scan(&total, &onTime, &late1To5, &late5To10, &late10To30, &lateOver30)

	if err != nil || total == 0 {
		return nil
	}

	t := float64(total)
	return &DelayDistribution{
		TotalRecords:      total,
		OnTimePercent:     (float64(onTime) / t) * 100.0,
		Late1To5Percent:   (float64(late1To5) / t) * 100.0,
		Late5To10Percent:  (float64(late5To10) / t) * 100.0,
		Late10To30Percent: (float64(late10To30) / t) * 100.0,
		LateOver30Percent: (float64(lateOver30) / t) * 100.0,
	}
}

func (s *Server) GetActiveTrains(w http.ResponseWriter, r *http.Request) {
	timeParam := r.URL.Query().Get("time")
	zgNow := getZagrebTime()
	secondsOfDay := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()

	if timeParam == "" {
		s.activeTrainsCacheMu.RLock()
		if s.activeTrainsCache != nil && time.Since(s.activeTrainsCache.timestamp) < 2*time.Second {
			data := s.activeTrainsCache.data
			s.activeTrainsCacheMu.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
			return
		}
		s.activeTrainsCacheMu.RUnlock()
	}

	secondsOfDay = parseTimeParam(timeParam, secondsOfDay)

	activeTrains, err := s.ComputeActiveTrains(secondsOfDay)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data, err := json.Marshal(activeTrains)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if timeParam == "" {
		s.activeTrainsCacheMu.Lock()
		s.activeTrainsCache = &activeTrainsCacheEntry{
			data:      data,
			timestamp: time.Now(),
		}
		s.activeTrainsCacheMu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	radAvgLat := ((lat1 + lat2) / 2.0) * (3.141592653589793 / 180.0)
	cosLat := 1.0 - (radAvgLat*radAvgLat)/2.0 + (radAvgLat*radAvgLat*radAvgLat*radAvgLat)/24.0
	dx := (lon2 - lon1) * 40000.0 * cosLat / 360.0
	dy := (lat2 - lat1) * 40000.0 / 360.0
	val := dx*dx + dy*dy
	if val <= 0 {
		return 0
	}
	x := val
	for i := 0; i < 10; i++ {
		x = (x + val/x) / 2.0
	}
	return x
}

func (s *Server) ComputeSegments() []SegmentSpeed {
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM route_segments").Scan(&count)
	if count == 0 {
		var stCount int
		_ = s.db.QueryRow("SELECT COUNT(*) FROM stop_times").Scan(&stCount)
		if stCount > 0 {
			_ = db.PopulateRouteSegments(s.db)
		}
	}

	query := `
		SELECT rs.from_stop_id, s1.stop_name, s1.stop_lat, s1.stop_lon,
		       rs.to_stop_id, s2.stop_name, s2.stop_lat, s2.stop_lon,
		       rs.train_count, rs.avg_duration_mins
		FROM route_segments rs
		JOIN stations s1 ON rs.from_stop_id = s1.stop_id
		JOIN stations s2 ON rs.to_stop_id = s2.stop_id
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return []SegmentSpeed{}
	}
	defer rows.Close()

	var segments []SegmentSpeed

	for rows.Next() {
		var (
			fromID, fromName string
			fromLat, fromLon float64
			toID, toName     string
			toLat, toLon     float64
			trainCount       int
			avgDurationMins  float64
		)

		if err := rows.Scan(&fromID, &fromName, &fromLat, &fromLon, &toID, &toName, &toLat, &toLon, &trainCount, &avgDurationMins); err != nil {
			continue
		}

		distKm := haversineKm(fromLat, fromLon, toLat, toLon)
		if distKm < 0.1 {
			continue
		}

		avgSpeedKmh := 0.0
		if avgDurationMins > 0 {
			avgSpeedKmh = distKm / (avgDurationMins / 60.0)
		}

		segID := fromID + "->" + toID
		segPath := s.getRouteWaypoints(fromID, toID, fromLat, fromLon, toLat, toLon)

		segments = append(segments, SegmentSpeed{
			ID:           segID,
			FromStopID:   fromID,
			FromStopName: fromName,
			FromLat:      fromLat,
			FromLon:      fromLon,
			ToStopID:     toID,
			ToStopName:   toName,
			ToLat:        toLat,
			ToLon:        toLon,
			DistanceKm:   distKm,
			AvgSpeedKmh:  avgSpeedKmh,
			TrainCount:   trainCount,
			Path:         segPath,
		})
	}

	return segments
}

func (s *Server) GetSegments(w http.ResponseWriter, r *http.Request) {
	s.segmentsMu.RLock()
	rawJSON := s.segmentsJSON
	etag := s.segmentsEtag
	s.segmentsMu.RUnlock()

	if len(rawJSON) == 0 {
		_ = s.GetCachedSegments()
		s.segmentsMu.RLock()
		rawJSON = s.segmentsJSON
		etag = s.segmentsEtag
		s.segmentsMu.RUnlock()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if etag != "" {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	w.Write(rawJSON)
}

func (s *Server) GetSegmentDetails(w http.ResponseWriter, r *http.Request) {
	fromID := strings.TrimSpace(r.URL.Query().Get("from"))
	toID := strings.TrimSpace(r.URL.Query().Get("to"))

	if fromID == "" || toID == "" {
		segID := strings.TrimSpace(r.URL.Query().Get("id"))
		parts := strings.Split(segID, "->")
		if len(parts) == 2 {
			fromID = parts[0]
			toID = parts[1]
		}
	}

	if fromID == "" || toID == "" {
		http.Error(w, "Parameters 'from' and 'to' (or 'id') are required", http.StatusBadRequest)
		return
	}

	query := `
		SELECT st1.stop_id, s1.stop_name, s1.stop_lat, s1.stop_lon,
		       st2.stop_id, s2.stop_name, s2.stop_lat, s2.stop_lon,
		       COALESCE(t.trip_short_name, t.trip_id), t.trip_id, COALESCE(t.trip_headsign, ''),
		       st1.departure_seconds, st2.arrival_seconds
		FROM stop_times st1
		JOIN stop_times st2 ON st1.trip_id = st2.trip_id AND st2.stop_sequence = st1.stop_sequence + 1
		JOIN stations s1 ON st1.stop_id = s1.stop_id
		JOIN stations s2 ON st2.stop_id = s2.stop_id
		JOIN trips t ON st1.trip_id = t.trip_id
		WHERE st1.stop_id = ? AND st2.stop_id = ?
	`

	rows, err := s.db.Query(query, fromID, toID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var seg *SegmentSpeed

	for rows.Next() {
		var (
			fID, fName                 string
			fLat, fLon                 float64
			tID, tName                 string
			tLat, tLon                 float64
			trainNum, tripID, headsign string
			depSec, arrSec             int
		)

		if err := rows.Scan(&fID, &fName, &fLat, &fLon, &tID, &tName, &tLat, &tLon, &trainNum, &tripID, &headsign, &depSec, &arrSec); err != nil {
			continue
		}

		durationSec := arrSec - depSec
		if durationSec <= 0 {
			continue
		}

		distKm := haversineKm(fLat, fLon, tLat, tLon)
		if distKm < 0.1 {
			continue
		}

		durationMins := float64(durationSec) / 60.0
		speedKmh := distKm / (durationMins / 60.0)

		if seg == nil {
			segID := fID + "->" + tID
			segPath := s.getRouteWaypoints(fID, tID, fLat, fLon, tLat, tLon)
			seg = &SegmentSpeed{
				ID:           segID,
				FromStopID:   fID,
				FromStopName: fName,
				FromLat:      fLat,
				FromLon:      fLon,
				ToStopID:     tID,
				ToStopName:   tName,
				ToLat:        tLat,
				ToLon:        tLon,
				DistanceKm:   distKm,
				Path:         segPath,
				Trains:       make([]TrainSegmentSpeed, 0),
			}
		}

		isDup := false
		for _, tr := range seg.Trains {
			if tr.TrainNumber == trainNum {
				isDup = true
				break
			}
		}

		if !isDup {
			seg.Trains = append(seg.Trains, TrainSegmentSpeed{
				TrainNumber:     trainNum,
				TripID:          tripID,
				Headsign:        headsign,
				ScheduledDepSec: depSec,
				ScheduledArrSec: arrSec,
				DurationMinutes: durationMins,
				SpeedKmh:        speedKmh,
			})
		}
	}

	if seg == nil {
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}

	seg.TrainCount = len(seg.Trains)
	totalSpeed := 0.0
	for _, tr := range seg.Trains {
		totalSpeed += tr.SpeedKmh
	}
	if seg.TrainCount > 0 {
		seg.AvgSpeedKmh = totalSpeed / float64(seg.TrainCount)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(seg)
}

func (s *Server) FetchAndSaveTrainDelay(trainID string) (*DelayAPIResponse, error) {
	s.lastQueried.Store(trainID, time.Now())
	zgNow := getZagrebTime()
	todayStr := zgNow.Format("2006-01-02")

	var currentPosStatus string
	var currentDelay int
	var lastSt, nextSt string
	var updatedAtStr string
	cutoff6h := zgNow.Add(-6 * time.Hour).Format("2006-01-02 15:04:05")
	errDB := s.db.QueryRow("SELECT position_status, delay_minutes, last_station, next_station, DATE(updated_at) FROM train_delays WHERE train_number = ? AND updated_at >= ?", trainID, cutoff6h).Scan(&currentPosStatus, &currentDelay, &lastSt, &nextSt, &updatedAtStr)

	if errDB == nil && strings.EqualFold(strings.TrimSpace(currentPosStatus), "arrived") && updatedAtStr == todayStr {
		if cached, ok := s.delayCache.Load(trainID); ok {
			c := cached.(cachedDelay)
			if time.Since(c.timestamp) < 30*time.Second {
				return &c.response, nil
			}
		}
		trNumInt, _ := strconv.Atoi(trainID)
		resp := &DelayAPIResponse{
			Success: true,
		}
		resp.Data.TrainNumber = trNumInt
		resp.Data.DelayMinutes = &currentDelay
		resp.Data.PositionStatus = currentPosStatus
		resp.Data.LastStation = lastSt
		resp.Data.NextStation = &nextSt
		resp.Data.IsFinished = true
		return resp, nil
	}

	if cached, ok := s.delayCache.Load(trainID); ok {
		c := cached.(cachedDelay)
		if time.Since(c.timestamp) < 30*time.Second {
			return &c.response, nil
		}
	}

	url := fmt.Sprintf("https://hzpp.app/api/train-delay?trainId=%s", trainID)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		s.recordDelayServiceCall(false)
		if cached, ok := s.delayCache.Load(trainID); ok {
			c := cached.(cachedDelay)
			return &c.response, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.recordDelayServiceCall(false)
		return nil, err
	}

	var delayResp DelayAPIResponse
	if err := json.Unmarshal(body, &delayResp); err == nil && delayResp.Success {
		s.recordDelayServiceCall(true)
		s.delayCache.Store(trainID, cachedDelay{
			response:  delayResp,
			timestamp: time.Now(),
		})

		nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()

		var bounds scheduleBounds
		var errSchedule error
		if val, ok := s.scheduleBoundsCache.Load(trainID); ok {
			bounds = val.(scheduleBounds)
		} else {
			errSchedule = s.db.QueryRow(`
				SELECT MIN(st.departure_seconds), MAX(st.arrival_seconds)
				FROM stop_times st
				JOIN trips t ON st.trip_id = t.trip_id
				WHERE (t.trip_short_name = ? OR t.trip_id = ?)
			`, trainID, trainID).Scan(&bounds.firstDepSec, &bounds.lastArrSec)
			if errSchedule == nil {
				s.scheduleBoundsCache.Store(trainID, bounds)
			}
		}
		firstDepSec, lastArrSec := bounds.firstDepSec, bounds.lastArrSec

		isArrivedStatus := strings.EqualFold(strings.TrimSpace(delayResp.Data.PositionStatus), "arrived")

		delayMins := 0
		if delayResp.Data.DelayMinutes != nil && *delayResp.Data.DelayMinutes > 0 {
			delayMins = *delayResp.Data.DelayMinutes
		} else if isArrivedStatus {
			// Fallback to previous recorded delay if delay is 0 or null upon arrival
			var prevDelay int
			if errPrev := s.db.QueryRow("SELECT delay_minutes FROM train_delays WHERE train_number = ?", trainID).Scan(&prevDelay); errPrev == nil && prevDelay > 0 {
				delayMins = prevDelay
			} else if delayResp.Data.DelayMinutes != nil {
				delayMins = *delayResp.Data.DelayMinutes
			}
		} else if delayResp.Data.DelayMinutes != nil {
			delayMins = *delayResp.Data.DelayMinutes
		}
		nextSt := ""
		if delayResp.Data.NextStation != nil {
			nextSt = *delayResp.Data.NextStation
		}

		isActiveTrain := true
		if errSchedule == nil {
			effectiveArrSec := lastArrSec + (delayMins * 60)
			if (nowSec < firstDepSec || nowSec > effectiveArrSec+7200) && !isArrivedStatus {
				isActiveTrain = false
			}
		}

		if isActiveTrain {
			nowZagrebStr := zgNow.Format("2006-01-02 15:04:05")
			if isArrivedStatus && errSchedule == nil && lastArrSec > 0 {
				effectiveSec := lastArrSec + (delayMins * 60)
				baseDate := zgNow
				if nowSec < firstDepSec {
					baseDate = baseDate.AddDate(0, 0, -1)
				}
				dayBase := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), 0, 0, 0, 0, baseDate.Location())
				nowZagrebStr = dayBase.Add(time.Duration(effectiveSec) * time.Second).Format("2006-01-02 15:04:05")
			}

			var lastHistDelay int
			var lastHistStatus, lastHistLastSt, lastHistNextSt string
			var lastHistTimeStr string
			errHist := s.db.QueryRow(`
				SELECT delay_minutes, COALESCE(position_status, ''), COALESCE(last_station, ''), COALESCE(next_station, ''), CAST(updated_at AS TEXT)
				FROM historical_train_delays
				WHERE train_number = ?
				ORDER BY id DESC LIMIT 1
			`, trainID).Scan(&lastHistDelay, &lastHistStatus, &lastHistLastSt, &lastHistNextSt, &lastHistTimeStr)

			if errHist == nil {
				loc := zagrebLoc
				lastHistTime, parseErr := time.ParseInLocation("2006-01-02 15:04:05", lastHistTimeStr, loc)
				if parseErr != nil {
					lastHistTime, parseErr = time.ParseInLocation(time.RFC3339, lastHistTimeStr, loc)
				}

				if parseErr == nil {
					elapsedMins := getZagrebTime().Sub(lastHistTime).Minutes()
					if elapsedMins < 0 {
						elapsedMins = -elapsedMins
					}
					diff := delayMins - lastHistDelay
					if diff < 0 {
						diff = -diff
					}
					if elapsedMins < 10.0 && diff > 45 {
						var confirmed bool
						if val, ok := s.pendingSpikes.Load(trainID); ok {
							pending := val.(pendingSpikeRecord)
							if time.Since(pending.timestamp) < 15*time.Minute {
								pDiff := pending.delayMinutes - delayMins
								if pDiff < 0 {
									pDiff = -pDiff
								}
								if pDiff <= 15 {
									confirmed = true
								}
							}
						}

						if !confirmed {
							s.pendingSpikes.Store(trainID, pendingSpikeRecord{
								delayMinutes:   delayMins,
								lastStation:    delayResp.Data.LastStation,
								nextStation:    nextSt,
								positionStatus: delayResp.Data.PositionStatus,
								timestamp:      time.Now(),
							})
							return &delayResp, nil
						} else {
							s.pendingSpikes.Delete(trainID)
						}
					} else {
						s.pendingSpikes.Delete(trainID)
					}
				}
			}

			// Check if we already recorded an 'arrived' status today for this train
			var existingArrivedCount int
			_ = s.db.QueryRow(`
				SELECT COUNT(*) FROM historical_train_delays
				WHERE train_number = ? AND delay_date = ? AND LOWER(position_status) = 'arrived'
			`, trainID, todayStr).Scan(&existingArrivedCount)

			shouldRecordHist := false
			if isArrivedStatus {
				if existingArrivedCount == 0 {
					shouldRecordHist = true
				}
			} else {
				shouldRecordHist = (errHist != nil) ||
					(lastHistDelay != delayMins) ||
					(lastHistStatus != delayResp.Data.PositionStatus) ||
					(lastHistLastSt != delayResp.Data.LastStation) ||
					(lastHistNextSt != nextSt)
			}

			_, _ = s.db.Exec(`
				INSERT INTO train_delays (train_number, delay_minutes, position_status, last_station, next_station, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(train_number) DO UPDATE SET
					delay_minutes=excluded.delay_minutes,
					position_status=excluded.position_status,
					last_station=excluded.last_station,
					next_station=excluded.next_station,
					updated_at=excluded.updated_at
			`, trainID, delayMins, delayResp.Data.PositionStatus, delayResp.Data.LastStation, nextSt, nowZagrebStr)

			if shouldRecordHist {
				_, _ = s.db.Exec(`
					INSERT INTO historical_train_delays (train_number, delay_date, delay_minutes, position_status, last_station, next_station, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?)
				`, trainID, todayStr, delayMins, delayResp.Data.PositionStatus, delayResp.Data.LastStation, nextSt, nowZagrebStr)
			}

			s.invalidateActiveTrainsCache()

			eventJSON, _ := json.Marshal(map[string]interface{}{
				"type":                "delay_update",
				"train_number":        trainID,
				"delay_minutes":       delayMins,
				"position_status":     delayResp.Data.PositionStatus,
				"last_station":        delayResp.Data.LastStation,
				"next_station":        nextSt,
				"timestamp":           getZagrebTime().Format("15:04:05"),
				"delay_service_stats": s.GetDelayServiceStats(),
			})
			s.broadcaster.Broadcast(string(eventJSON))
		}

		return &delayResp, nil
	}

	return nil, fmt.Errorf("failed to parse live delay for train %s", trainID)
}

func (s *Server) StreamDelays(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.broadcaster.Subscribe()
	defer s.broadcaster.Unsubscribe(ch)

	initJSON, _ := json.Marshal(map[string]interface{}{
		"type":                "connected",
		"message":             "Connected to HŽ real-time delay update stream",
		"timestamp":           getZagrebTime().Format("15:04:05"),
		"delay_service_stats": s.GetDelayServiceStats(),
	})
	fmt.Fprintf(w, "data: %s\n\n", initJSON)
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (s *Server) FetchTrainDelay(w http.ResponseWriter, r *http.Request) {
	trainID := strings.TrimSpace(r.URL.Query().Get("trainId"))
	if trainID == "" {
		http.Error(w, "trainId parameter is required", http.StatusBadRequest)
		return
	}

	if len(trainID) > 20 || strings.ContainsAny(trainID, "/\\?%*:|\"<>;") {
		http.Error(w, "invalid trainId parameter", http.StatusBadRequest)
		return
	}

	delayResp, err := s.FetchAndSaveTrainDelay(trainID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch live delay: %v", err), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(delayResp)
}

func (s *Server) autoGenerateMissingArrivals() {
	zgNow := getZagrebTime()
	nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()
	todayStr := zgNow.Format("2006-01-02")

	calCond, calArgs := getCalendarCondition(zgNow)

	// Query trains running today whose final scheduled arrival time + delay + 10 mins buffer has passed
	query := fmt.Sprintf(`
		SELECT COALESCE(t.trip_short_name, t.trip_id) as tr_num, MAX(st.arrival_seconds) as last_arr_sec, s.stop_name as dest_name, td.delay_minutes
		FROM stop_times st
		JOIN trips t ON st.trip_id = t.trip_id
		LEFT JOIN calendar c ON t.service_id = c.service_id
		JOIN stations s ON st.stop_id = s.stop_id
		JOIN train_delays td ON td.train_number = COALESCE(t.trip_short_name, t.trip_id)
		WHERE %s
		  AND (td.position_status IS NULL OR LOWER(td.position_status) != 'arrived')
		GROUP BY tr_num
		HAVING (last_arr_sec + COALESCE(td.delay_minutes, 0) * 60 + 600) <= ?
	`, calCond)

	args := append(calArgs, nowSec)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return
	}
	defer rows.Close()

	type unarrivedTrain struct {
		trainNum   string
		lastArrSec int
		destName   string
		delayMins  int
	}
	var unarrived []unarrivedTrain

	for rows.Next() {
		var u unarrivedTrain
		if err := rows.Scan(&u.trainNum, &u.lastArrSec, &u.destName, &u.delayMins); err == nil {
			unarrived = append(unarrived, u)
		}
	}

	if len(unarrived) == 0 {
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	stmtUpdate, err := tx.Prepare(`
		UPDATE train_delays
		SET position_status = 'arrived',
			last_station = ?,
			next_station = '',
			updated_at = ?
		WHERE train_number = ?
	`)
	if err != nil {
		return
	}
	defer stmtUpdate.Close()

	stmtCheckHist, err := tx.Prepare(`
		SELECT COUNT(*) FROM historical_train_delays
		WHERE train_number = ? AND delay_date = ? AND LOWER(position_status) = 'arrived'
	`)
	if err != nil {
		return
	}
	defer stmtCheckHist.Close()

	stmtInsertHist, err := tx.Prepare(`
		INSERT INTO historical_train_delays (train_number, delay_date, delay_minutes, position_status, last_station, next_station, updated_at)
		VALUES (?, ?, ?, 'arrived', ?, '', ?)
	`)
	if err != nil {
		return
	}
	defer stmtInsertHist.Close()

	for _, u := range unarrived {
		effectiveSec := u.lastArrSec + (u.delayMins * 60)
		dayBase := time.Date(zgNow.Year(), zgNow.Month(), zgNow.Day(), 0, 0, 0, 0, zgNow.Location())
		arrTimestampStr := dayBase.Add(time.Duration(effectiveSec) * time.Second).Format("2006-01-02 15:04:05")

		_, _ = stmtUpdate.Exec(u.destName, arrTimestampStr, u.trainNum)

		var existingArrived int
		_ = stmtCheckHist.QueryRow(u.trainNum, todayStr).Scan(&existingArrived)

		if existingArrived == 0 {
			_, _ = stmtInsertHist.Exec(u.trainNum, todayStr, u.delayMins, u.destName, arrTimestampStr)
		}
	}

	_ = tx.Commit()
}

func (s *Server) StartBackgroundDelayWorker() {
	ticker := time.NewTicker(20 * time.Second)
	tickCount := 0
	go func() {
		for range ticker.C {
			tickCount++
			zgNow := getZagrebTime()
			nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()
			todayStr := zgNow.Format("2006-01-02")

			if tickCount%6 == 1 { // Runs every 2 minutes (6 * 20s)
				s.autoGenerateMissingArrivals()
			}

			calCond, calArgs := getCalendarCondition(zgNow)

			query := fmt.Sprintf(`
				SELECT DISTINCT COALESCE(t.trip_short_name, t.trip_id)
				FROM stop_times st1
				JOIN stop_times st2 ON st1.trip_id = st2.trip_id AND st2.stop_sequence = st1.stop_sequence + 1
				JOIN trips t ON st1.trip_id = t.trip_id
				LEFT JOIN calendar c ON t.service_id = c.service_id
				LEFT JOIN train_delays td ON td.train_number = COALESCE(t.trip_short_name, t.trip_id)
				WHERE st1.departure_seconds <= ?
				  AND (st2.arrival_seconds + COALESCE(td.delay_minutes, 0) * 60) >= ?
				  AND %s
				  AND (
				    td.position_status IS NULL
				    OR LOWER(td.position_status) != 'arrived'
				    OR DATE(td.updated_at) != ?
				  )
			`, calCond)

			args := append([]interface{}{nowSec, nowSec}, calArgs...)
			args = append(args, todayStr)

			rows, err := s.db.Query(query, args...)
			if err != nil {
				continue
			}

			var trainNums []string
			for rows.Next() {
				var tn string
				if err := rows.Scan(&tn); err == nil && tn != "" {
					trainNums = append(trainNums, tn)
				}
			}
			rows.Close()

			for _, tn := range trainNums {
				if val, ok := s.lastQueried.Load(tn); ok {
					if lastTime, ok := val.(time.Time); ok && time.Since(lastTime) < 60*time.Second {
						continue
					}
				}

				_, _ = s.FetchAndSaveTrainDelay(tn)
				time.Sleep(3 * time.Second)
			}
		}
	}()
}

func (s *Server) PlanRoute(w http.ResponseWriter, r *http.Request) {
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))

	if from == "" || to == "" {
		http.Error(w, "both 'from' and 'to' station parameters are required", http.StatusBadRequest)
		return
	}

	zgNow := getZagrebTime()
	nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()
	nowSec = parseTimeParam(r.URL.Query().Get("time"), nowSec)

	calCond, calArgs := getCalendarCondition(zgNow)

	var fromIDs, toIDs []string

	if row := s.db.QueryRow("SELECT stop_id FROM stations WHERE stop_id = ? OR stop_name = ?", from, from); true {
		var id string
		if err := row.Scan(&id); err == nil {
			fromIDs = append(fromIDs, id)
		}
	}
	if len(fromIDs) == 0 {
		likeFrom := "%" + from + "%"
		if rows, err := s.db.Query("SELECT stop_id FROM stations WHERE stop_name LIKE ?", likeFrom); err == nil {
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil {
					fromIDs = append(fromIDs, id)
				}
			}
			rows.Close()
		}
	}

	if row := s.db.QueryRow("SELECT stop_id FROM stations WHERE stop_id = ? OR stop_name = ?", to, to); true {
		var id string
		if err := row.Scan(&id); err == nil {
			toIDs = append(toIDs, id)
		}
	}
	if len(toIDs) == 0 {
		likeTo := "%" + to + "%"
		if rows, err := s.db.Query("SELECT stop_id FROM stations WHERE stop_name LIKE ?", likeTo); err == nil {
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err == nil {
					toIDs = append(toIDs, id)
				}
			}
			rows.Close()
		}
	}

	if len(fromIDs) == 0 || len(toIDs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]RoutePlanEntry{})
		return
	}

	directQuery := fmt.Sprintf(`
		SELECT MIN(st1.trip_id), COALESCE(t.trip_short_name, t.trip_id) as train_num, COALESCE(t.trip_headsign, ''),
		       s1.stop_name as origin_name, s2.stop_name as dest_name,
		       st1.departure_time, st2.arrival_time, st1.departure_seconds,
		       (st2.arrival_seconds - st1.departure_seconds) / 60 as duration,
		       COALESCE(td.delay_minutes, 0) as delay_mins
		FROM stop_times st1
		JOIN stop_times st2 ON st1.trip_id = st2.trip_id AND st2.stop_sequence > st1.stop_sequence
		JOIN stations s1 ON st1.stop_id = s1.stop_id
		JOIN stations s2 ON st2.stop_id = s2.stop_id
		JOIN trips t ON st1.trip_id = t.trip_id
		LEFT JOIN calendar c ON t.service_id = c.service_id
		LEFT JOIN train_delays td ON td.train_number = COALESCE(t.trip_short_name, t.trip_id)
		WHERE st1.stop_id IN (%s) AND st2.stop_id IN (%s) AND %s
		GROUP BY train_num, st1.departure_seconds, st2.arrival_seconds
		ORDER BY st1.departure_seconds ASC
	`, makePlaceholders(len(fromIDs)), makePlaceholders(len(toIDs)), calCond)

	args := append(toInterfaceSlice(fromIDs), toInterfaceSlice(toIDs)...)
	args = append(args, calArgs...)

	allNotices := s.fetchNoticesFromDB()

	rows, err := s.db.Query(directQuery, args...)
	results := make([]RoutePlanEntry, 0)
	if err == nil {
		for rows.Next() {
			var entry RoutePlanEntry
			if err := rows.Scan(&entry.TripID, &entry.TrainNumber, &entry.Headsign, &entry.OriginStationName, &entry.DestinationStationName, &entry.DepartureTime, &entry.ArrivalTime, &entry.DepartureSeconds, &entry.DurationMinutes, &entry.DelayMinutes); err == nil {
				if entry.DelayMinutes == 0 {
					if del, found := s.getTrainDelayMinutes(entry.TrainNumber); found {
						entry.DelayMinutes = del
					}
				}

				todayStr := zgNow.Format("2006-01-02")
				stNames := s.getTripStationNames(entry.TripID)
				if len(stNames) == 0 {
					stNames = []string{entry.OriginStationName, entry.DestinationStationName}
				}
				for _, n := range allNotices {
					matched, isBus, isWorks, noticeID := s.isNoticeApplicable(n, entry.TrainNumber, stNames, todayStr)
					if matched {
						entry.AffectedByDisruption = true
						if isBus {
							entry.IsBusReplacement = true
						}
						if isWorks {
							entry.IsTrackWorks = true
						}
						if entry.NoticeID == "" {
							entry.NoticeID = noticeID
						}
						if entry.DisruptionNotice == "" {
							entry.DisruptionNotice = n.Title
						} else {
							entry.DisruptionNotice += " | " + n.Title
						}
					}
				}

				results = append(results, entry)
			}
		}
		rows.Close()
	}

	if len(results) == 0 {
		transferQuery := fmt.Sprintf(`
			SELECT st1.trip_id, COALESCE(t1.trip_short_name, t1.trip_id), COALESCE(t1.trip_headsign, ''),
			       s1.stop_name, st1.departure_time, st1.departure_seconds,
			       st2.trip_id, COALESCE(t2.trip_short_name, t2.trip_id), COALESCE(t2.trip_headsign, ''),
			       s2.stop_name, st2.arrival_time, st2.arrival_seconds,
			       st1_arr.arrival_seconds as leg1_arr_sec, st2_dep.departure_seconds as leg2_dep_sec,
			       st1_arr.arrival_time as leg1_arr_time, st2_dep.departure_time as leg2_dep_time,
			       s_trans.stop_name as transfer_name
			FROM stop_times st1
			JOIN stop_times st1_arr ON st1.trip_id = st1_arr.trip_id AND st1_arr.stop_sequence > st1.stop_sequence
			JOIN stop_times st2_dep ON st1_arr.stop_id = st2_dep.stop_id
			JOIN stop_times st2 ON st2_dep.trip_id = st2.trip_id AND st2.stop_sequence > st2_dep.stop_sequence
			JOIN stations s1 ON st1.stop_id = s1.stop_id
			JOIN stations s2 ON st2.stop_id = s2.stop_id
			JOIN stations s_trans ON st1_arr.stop_id = s_trans.stop_id
			JOIN trips t1 ON st1.trip_id = t1.trip_id
			JOIN trips t2 ON st2_dep.trip_id = t2.trip_id
			LEFT JOIN calendar c1 ON t1.service_id = c1.service_id
			LEFT JOIN calendar c2 ON t2.service_id = c2.service_id
			WHERE st1.stop_id IN (%s) AND st2.stop_id IN (%s)
			  AND st1.trip_id != st2.trip_id
			  AND st2_dep.departure_seconds >= (st1_arr.arrival_seconds + 180)
			  AND st2_dep.departure_seconds <= (st1_arr.arrival_seconds + 10800)
			ORDER BY st1.departure_seconds ASC
			LIMIT 10
		`, makePlaceholders(len(fromIDs)), makePlaceholders(len(toIDs)))

		transArgs := append(toInterfaceSlice(fromIDs), toInterfaceSlice(toIDs)...)
		if transRows, err := s.db.Query(transferQuery, transArgs...); err == nil {
			for transRows.Next() {
				var (
					t1ID, tr1Num, h1, origName, dep1Time                                   string
					dep1Sec                                                                 int
					t2ID, tr2Num, h2, destName, arr2Time                                   string
					arr2Sec, leg1ArrSec, leg2DepSec                                         int
					leg1ArrTime, leg2DepTime, transName                                     string
				)
				if err := transRows.Scan(&t1ID, &tr1Num, &h1, &origName, &dep1Time, &dep1Sec, &t2ID, &tr2Num, &h2, &destName, &arr2Time, &arr2Sec, &leg1ArrSec, &leg2DepSec, &leg1ArrTime, &leg2DepTime, &transName); err == nil {
					totalDur := (arr2Sec - dep1Sec) / 60
					entry := RoutePlanEntry{
						TripID:                 t1ID + "+" + t2ID,
						TrainNumber:            tr1Num + " ➔ " + tr2Num,
						Headsign:               fmt.Sprintf("Change at %s (dep %s)", transName, stripSeconds(leg2DepTime)),
						OriginStationName:      origName,
						DestinationStationName: destName,
						DepartureTime:          dep1Time,
						ArrivalTime:            arr2Time,
						DepartureSeconds:       dep1Sec,
						DurationMinutes:        totalDur,
						IsTransfer:             true,
						TransferStationName:    transName,
					}
					results = append(results, entry)
				}
			}
			transRows.Close()
		}
	}

	nearestIdx := -1
	minFutureDiff := 86400 * 2

	for i, r := range results {
		diff := r.DepartureSeconds - nowSec
		if diff >= 0 && diff < minFutureDiff {
			minFutureDiff = diff
			nearestIdx = i
		}
	}

	if nearestIdx == -1 && len(results) > 0 {
		nearestIdx = 0
	}

	if nearestIdx >= 0 && nearestIdx < len(results) {
		results[nearestIdx].IsNearestFuture = true
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) GetDashboardStats(w http.ResponseWriter, r *http.Request) {
	zgNow := getZagrebTime()
	nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()
	nowSec = parseTimeParam(r.URL.Query().Get("time"), nowSec)

	var stats DashboardStats

	_ = s.db.QueryRow("SELECT COUNT(*) FROM stations").Scan(&stats.TotalStations)

	calCond, calArgs := getCalendarCondition(zgNow)
	tripsQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT t.trip_id)
		FROM trips t
		LEFT JOIN calendar c ON t.service_id = c.service_id
		WHERE %s
	`, calCond)
	_ = s.db.QueryRow(tripsQuery, calArgs...).Scan(&stats.TripsRunningToday)

	activeTrains, err := s.ComputeActiveTrains(nowSec)
	if err != nil {
		activeTrains = []ActiveTrain{}
	}

	stats.ActiveTrainCount = len(activeTrains)
	totalDelay := 0
	maxDelay := 0
	recentDelayed := make([]ActiveTrain, 0)

	for _, tr := range activeTrains {
		if tr.DelayMinutes > 0 {
			stats.DelayedTrainCount++
			totalDelay += tr.DelayMinutes
			if tr.DelayMinutes > maxDelay {
				maxDelay = tr.DelayMinutes
			}
			recentDelayed = append(recentDelayed, tr)
		} else {
			stats.OnTimeTrainCount++
		}
	}

	stats.MaxDelayMinutes = maxDelay
	if stats.DelayedTrainCount > 0 {
		stats.AvgDelayMinutes = float64(totalDelay) / float64(stats.DelayedTrainCount)
	}
	stats.RecentDelayedTrains = recentDelayed

	dsStats := s.GetDelayServiceStats()
	stats.DelayServiceStats = &dsStats

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
