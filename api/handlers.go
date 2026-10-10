package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

type cachedDelay struct {
	response  DelayAPIResponse
	timestamp time.Time
}

type Server struct {
	db            *sql.DB
	osmGraph      *OSMGraph
	delayCache    sync.Map
	routeCache    sync.Map
	segmentsCache []SegmentSpeed
	segmentsMu    sync.RWMutex
	broadcaster   *Broadcaster
}

func NewServer(db *sql.DB) *Server {
	return &Server{
		db:          db,
		broadcaster: NewBroadcaster(),
	}
}

func getZagrebTime() time.Time {
	loc, err := time.LoadLocation("Europe/Zagreb")
	if err != nil {
		loc = time.FixedZone("CEST", 2*3600)
	}
	return time.Now().In(loc)
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
		return parts[0] + ":" + parts[1]
	}
	return timeStr
}

func (s *Server) SetOSMGraph(graph *OSMGraph) {
	s.osmGraph = graph
	s.routeCache = sync.Map{}
	s.InvalidateSegmentsCache()
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
	return s.segmentsCache
}

func (s *Server) InvalidateSegmentsCache() {
	s.segmentsMu.Lock()
	s.segmentsCache = nil
	s.segmentsMu.Unlock()
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
		SELECT DISTINCT COALESCE(t.trip_short_name, t.trip_id) as train_num, COALESCE(t.trip_headsign, ''),
		       s.stop_lat, s.stop_lon
		FROM trips t
		JOIN stop_times st ON t.trip_id = st.trip_id AND st.stop_sequence = 1
		JOIN stations s ON st.stop_id = s.stop_id
		WHERE t.trip_short_name LIKE ? OR t.trip_id LIKE ? OR t.trip_headsign LIKE ?
		ORDER BY CASE WHEN t.trip_short_name = ? THEN 0 ELSE 1 END, train_num ASC
		LIMIT 10
	`, likeQ, likeQ, likeQ, q)
	if err == nil {
		for trRows.Next() {
			var trNum, headsign string
			var lat, lon float64
			if err := trRows.Scan(&trNum, &headsign, &lat, &lon); err == nil {
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
		err := s.db.QueryRow("SELECT delay_minutes FROM train_delays WHERE train_number = ? AND updated_at >= datetime('now', '-6 hours')", entry.TrainNumber).Scan(&delayMins)
		if err == nil {
			entry.DelayMinutes = &delayMins
		}

		entries = append(entries, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (s *Server) ComputeActiveTrains(secondsOfDay int) ([]ActiveTrain, error) {
	zgNow := getZagrebTime()

	delayMap := make(map[string]int)
	if delayRows, err := s.db.Query("SELECT train_number, delay_minutes FROM train_delays WHERE updated_at >= datetime('now', '-6 hours')"); err == nil {
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

	query := fmt.Sprintf(`
		SELECT t.trip_id, COALESCE(t.trip_short_name, t.trip_id), COALESCE(t.trip_headsign, ''),
		       st1.stop_id, s1.stop_name, s1.stop_lat, s1.stop_lon, st1.departure_seconds,
		       st2.stop_id, s2.stop_name, s2.stop_lat, s2.stop_lon, st2.arrival_seconds
		FROM stop_times st1
		JOIN stop_times st2 ON st1.trip_id = st2.trip_id AND st2.stop_sequence = st1.stop_sequence + 1
		JOIN stations s1 ON st1.stop_id = s1.stop_id
		JOIN stations s2 ON st2.stop_id = s2.stop_id
		JOIN trips t ON st1.trip_id = t.trip_id
		LEFT JOIN calendar c ON t.service_id = c.service_id
		WHERE st1.departure_seconds <= ? AND st2.arrival_seconds >= ? AND %s
	`, calCond)

	args := append([]interface{}{windowEnd, windowStart}, calArgs...)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	activeTrainsMap := make(map[string]ActiveTrain)
	var activeTripIDs []string
	tripIDToTrainNum := make(map[string]string)

	for rows.Next() {
		var (
			tripID, trainNum, headsign string
			s1ID, s1Name               string
			s1Lat, s1Lon               float64
			s1DepSec                   int
			s2ID, s2Name               string
			s2Lat, s2Lon               float64
			s2ArrSec                   int
		)

		if err := rows.Scan(&tripID, &trainNum, &headsign, &s1ID, &s1Name, &s1Lat, &s1Lon, &s1DepSec, &s2ID, &s2Name, &s2Lat, &s2Lon, &s2ArrSec); err != nil {
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

			pathWaypoints := s.getRouteWaypoints(s1ID, s2ID, s1Lat, s1Lon, s2Lat, s2Lon)
			curLat, curLon := interpolateAlongPath(pathWaypoints, progress)

			if existing, ok := activeTrainsMap[trainNum]; !ok || (progress > 0 && progress < 1 && (existing.Progress <= 0 || existing.Progress >= 1)) {
				if !ok {
					activeTripIDs = append(activeTripIDs, tripID)
					tripIDToTrainNum[tripID] = trainNum
				}
				activeTrainsMap[trainNum] = ActiveTrain{
					TripID:          tripID,
					TrainNumber:     trainNum,
					Headsign:        headsign,
					CurrentLat:      curLat,
					CurrentLon:      curLon,
					PrevStationID:   s1ID,
					PrevStationName: s1Name,
					NextStationID:   s2ID,
					NextStationName: s2Name,
					SegmentID:       s1ID + "->" + s2ID,
					Progress:        progress,
					Status:          status,
					DelayMinutes:    delayMinutes,
					ScheduledDepSec: s1DepSec,
					ScheduledArrSec: s2ArrSec,
				}
			}
		}
	}

	if len(activeTripIDs) > 0 {
		placeholders := make([]string, len(activeTripIDs))
		args := make([]interface{}, len(activeTripIDs))
		for i, id := range activeTripIDs {
			placeholders[i] = "?"
			args[i] = id
		}
		terminalQuery := fmt.Sprintf(`
			SELECT st.trip_id, st.stop_sequence, s.stop_name
			FROM stop_times st
			JOIN stations s ON st.stop_id = s.stop_id
			WHERE st.trip_id IN (%s) AND (
				st.stop_sequence = 1 OR st.stop_sequence = (
					SELECT MAX(stop_sequence) FROM stop_times WHERE trip_id = st.trip_id
				)
			)
			ORDER BY st.trip_id, st.stop_sequence ASC
		`, strings.Join(placeholders, ","))

		if termRows, err := s.db.Query(terminalQuery, args...); err == nil {
			defer termRows.Close()
			terminals := make(map[string][]string)
			for termRows.Next() {
				var tid, sname string
				var seq int
				if err := termRows.Scan(&tid, &seq, &sname); err == nil {
					terminals[tid] = append(terminals[tid], sname)
				}
			}
			for tid, names := range terminals {
				trNum := tripIDToTrainNum[tid]
				if tr, ok := activeTrainsMap[trNum]; ok {
					if len(names) > 0 {
						tr.FirstStationName = names[0]
					}
					if len(names) > 1 {
						tr.LastStationName = names[len(names)-1]
					} else if len(names) == 1 {
						tr.LastStationName = names[0]
					}
					if tr.FirstStationName == "" {
						tr.FirstStationName = tr.Headsign
					}
					if tr.LastStationName == "" {
						tr.LastStationName = tr.Headsign
					}
					activeTrainsMap[trNum] = tr
				}
			}
		}
	}

	activeTrains := make([]ActiveTrain, 0, len(activeTrainsMap))
	for _, train := range activeTrainsMap {
		activeTrains = append(activeTrains, train)
	}

	return activeTrains, nil
}

func (s *Server) GetTrainDetails(w http.ResponseWriter, r *http.Request) {
	trainNum := strings.TrimPrefix(r.URL.Path, "/api/trains/")
	trainNum = strings.TrimSpace(trainNum)
	if trainNum == "" {
		trainNum = strings.TrimSpace(r.URL.Query().Get("train_number"))
	}
	if trainNum == "" {
		http.Error(w, "train_number parameter is required", http.StatusBadRequest)
		return
	}

	zgNow := getZagrebTime()
	nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()

	var details TrainDetails
	details.TrainNumber = trainNum

	query := `
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
		WHERE COALESCE(t.trip_short_name, t.trip_id) = ? OR t.trip_id = ?
		LIMIT 1
	`

	err := s.db.QueryRow(query, trainNum, trainNum).Scan(
		&details.TripID, &details.Headsign,
		&details.FirstStationName, &details.FirstDepartureTime, &details.FirstDepartureSeconds,
		&details.LastStationName, &details.LastArrivalTime, &details.LastArrivalSeconds,
	)
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
	_ = s.db.QueryRow("SELECT delay_minutes, COALESCE(position_status, '') FROM train_delays WHERE train_number = ? AND updated_at >= datetime('now', '-6 hours')", trainNum).Scan(&delayMins, &posStatus)
	details.DelayMinutes = delayMins
	details.PositionStatus = posStatus

	activeTrains, _ := s.ComputeActiveTrains(nowSec)
	for _, tr := range activeTrains {
		if tr.TrainNumber == trainNum || tr.TripID == details.TripID {
			details.IsActive = true
			details.Lat = tr.CurrentLat
			details.Lon = tr.CurrentLon
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

func (s *Server) GetActiveTrains(w http.ResponseWriter, r *http.Request) {
	zgNow := getZagrebTime()
	secondsOfDay := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()
	secondsOfDay = parseTimeParam(r.URL.Query().Get("time"), secondsOfDay)

	activeTrains, err := s.ComputeActiveTrains(secondsOfDay)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(activeTrains)
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
	segments := s.GetCachedSegments()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(segments)
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
	zgNow := getZagrebTime()
	todayStr := zgNow.Format("2006-01-02")

	var currentPosStatus string
	var currentDelay int
	var lastSt, nextSt string
	var updatedAtStr string
	errDB := s.db.QueryRow("SELECT position_status, delay_minutes, last_station, next_station, DATE(updated_at) FROM train_delays WHERE train_number = ? AND updated_at >= datetime('now', '-6 hours')", trainID).Scan(&currentPosStatus, &currentDelay, &lastSt, &nextSt, &updatedAtStr)

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
	client := &http.Client{Timeout: 8 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		if cached, ok := s.delayCache.Load(trainID); ok {
			c := cached.(cachedDelay)
			return &c.response, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var delayResp DelayAPIResponse
	if err := json.Unmarshal(body, &delayResp); err == nil && delayResp.Success {
		s.delayCache.Store(trainID, cachedDelay{
			response:  delayResp,
			timestamp: time.Now(),
		})

		nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()

		var firstDepSec, lastArrSec int
		errSchedule := s.db.QueryRow(`
			SELECT MIN(st_first.departure_seconds), MAX(st_last.arrival_seconds)
			FROM stop_times st_first
			JOIN trips t ON st_first.trip_id = t.trip_id
			JOIN stop_times st_last ON st_last.trip_id = t.trip_id
			WHERE COALESCE(t.trip_short_name, t.trip_id) = ?
		`, trainID).Scan(&firstDepSec, &lastArrSec)

		delayMins := 0
		if delayResp.Data.DelayMinutes != nil {
			delayMins = *delayResp.Data.DelayMinutes
		}
		nextSt := ""
		if delayResp.Data.NextStation != nil {
			nextSt = *delayResp.Data.NextStation
		}

		isActiveTrain := true
		if errSchedule == nil {
			effectiveArrSec := lastArrSec + (delayMins * 60)
			if (nowSec < firstDepSec || nowSec > effectiveArrSec+7200) && !strings.EqualFold(strings.TrimSpace(delayResp.Data.PositionStatus), "arrived") {
				isActiveTrain = false
			}
		}

		if isActiveTrain {
			var prevDelay int
			var prevStatus string
			errPrev := s.db.QueryRow("SELECT delay_minutes, COALESCE(position_status, '') FROM train_delays WHERE train_number = ?", trainID).Scan(&prevDelay, &prevStatus)

			isChanged := (errPrev != nil) || (prevDelay != delayMins) || (prevStatus != delayResp.Data.PositionStatus)

			_, _ = s.db.Exec(`
				INSERT INTO train_delays (train_number, delay_minutes, position_status, last_station, next_station, updated_at)
				VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
				ON CONFLICT(train_number) DO UPDATE SET
					delay_minutes=excluded.delay_minutes,
					position_status=excluded.position_status,
					last_station=excluded.last_station,
					next_station=excluded.next_station,
					updated_at=CURRENT_TIMESTAMP
			`, trainID, delayMins, delayResp.Data.PositionStatus, delayResp.Data.LastStation, nextSt)

			if isChanged {
				_, _ = s.db.Exec(`
					INSERT INTO historical_train_delays (train_number, delay_date, delay_minutes, position_status, last_station, next_station, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
				`, trainID, todayStr, delayMins, delayResp.Data.PositionStatus, delayResp.Data.LastStation, nextSt)
			}

			eventJSON, _ := json.Marshal(map[string]interface{}{
				"type":            "delay_update",
				"train_number":    trainID,
				"delay_minutes":   delayMins,
				"position_status": delayResp.Data.PositionStatus,
				"last_station":    delayResp.Data.LastStation,
				"next_station":    nextSt,
				"timestamp":       getZagrebTime().Format("15:04:05"),
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
		"type":      "connected",
		"message":   "Connected to HŽ real-time delay update stream",
		"timestamp": getZagrebTime().Format("15:04:05"),
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

func (s *Server) StartBackgroundDelayWorker() {
	lastQueried := make(map[string]time.Time)
	var mu sync.Mutex

	ticker := time.NewTicker(20 * time.Second)
	go func() {
		for range ticker.C {
			zgNow := getZagrebTime()
			nowSec := zgNow.Hour()*3600 + zgNow.Minute()*60 + zgNow.Second()
			todayStr := zgNow.Format("2006-01-02")

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
				mu.Lock()
				lastTime, exists := lastQueried[tn]
				if exists && time.Since(lastTime) < 60*time.Second {
					mu.Unlock()
					continue
				}
				lastQueried[tn] = time.Now()
				mu.Unlock()

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
		       (st2.arrival_seconds - st1.departure_seconds) / 60 as duration
		FROM stop_times st1
		JOIN stop_times st2 ON st1.trip_id = st2.trip_id AND st2.stop_sequence > st1.stop_sequence
		JOIN stations s1 ON st1.stop_id = s1.stop_id
		JOIN stations s2 ON st2.stop_id = s2.stop_id
		JOIN trips t ON st1.trip_id = t.trip_id
		LEFT JOIN calendar c ON t.service_id = c.service_id
		WHERE st1.stop_id IN (%s) AND st2.stop_id IN (%s) AND %s
		GROUP BY train_num, st1.departure_seconds, st2.arrival_seconds
		ORDER BY st1.departure_seconds ASC
	`, makePlaceholders(len(fromIDs)), makePlaceholders(len(toIDs)), calCond)

	args := append(toInterfaceSlice(fromIDs), toInterfaceSlice(toIDs)...)
	args = append(args, calArgs...)

	rows, err := s.db.Query(directQuery, args...)
	results := make([]RoutePlanEntry, 0)
	if err == nil {
		for rows.Next() {
			var entry RoutePlanEntry
			if err := rows.Scan(&entry.TripID, &entry.TrainNumber, &entry.Headsign, &entry.OriginStationName, &entry.DestinationStationName, &entry.DepartureTime, &entry.ArrivalTime, &entry.DepartureSeconds, &entry.DurationMinutes); err == nil {
				var delayMins int
				_ = s.db.QueryRow("SELECT delay_minutes FROM train_delays WHERE train_number = ? AND updated_at >= datetime('now', '-6 hours')", entry.TrainNumber).Scan(&delayMins)
				entry.DelayMinutes = delayMins
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
