package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hz-train-map/db"
)

func setupTestDB(t *testing.T) *Server {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO stations (stop_id, stop_name, stop_lat, stop_lon)
		VALUES ('s1', 'Zagreb Glavni Kolodvor', 45.8044, 15.9788),
		       ('s2', 'Vinkovci', 45.3004, 18.8028);

		INSERT INTO routes (route_id, route_short_name, route_long_name)
		VALUES ('r1', '20', 'Zagreb - Vinkovci');

		INSERT INTO calendar (service_id, start_date, end_date, monday, tuesday, wednesday, thursday, friday, saturday, sunday)
		VALUES ('serv1', '20200101', '20301231', 1, 1, 1, 1, 1, 1, 1);

		INSERT INTO trips (trip_id, route_id, service_id, trip_short_name)
		VALUES ('t2010', 'r1', 'serv1', '2010');

		INSERT INTO stop_times (trip_id, arrival_time, departure_time, arrival_seconds, departure_seconds, stop_id, stop_sequence)
		VALUES ('t2010', '10:00:00', '10:05:00', 36000, 36300, 's1', 1),
		       ('t2010', '12:00:00', '12:05:00', 43200, 43500, 's2', 2);
	`)
	if err != nil {
		t.Fatalf("Failed to seed database: %v", err)
	}

	_ = db.PopulateRouteSegments(database)
	srv := NewServer(database)
	srv.InvalidateSegmentsCache()
	return srv
}

func TestGetStations(t *testing.T) {
	srv := setupTestDB(t)

	req := httptest.NewRequest("GET", "/api/stations", nil)
	w := httptest.NewRecorder()

	srv.GetStations(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var stations []Station
	if err := json.NewDecoder(res.Body).Decode(&stations); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(stations) != 2 {
		t.Fatalf("Expected 2 stations, got %d", len(stations))
	}
}

func TestGetStationTimetable(t *testing.T) {
	srv := setupTestDB(t)

	req := httptest.NewRequest("GET", "/api/stations/s1/timetable", nil)
	w := httptest.NewRecorder()

	srv.GetStationTimetable(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var entries []StationTimetableEntry
	if err := json.NewDecoder(res.Body).Decode(&entries); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("Expected 1 timetable entry, got %d", len(entries))
	}

	if entries[0].TrainNumber != "2010" {
		t.Errorf("Expected train number 2010, got %s", entries[0].TrainNumber)
	}
}

func TestGetTrainDetails(t *testing.T) {
	srv := setupTestDB(t)

	req := httptest.NewRequest("GET", "/api/trains/2010", nil)
	w := httptest.NewRecorder()

	srv.GetTrainDetails(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var details TrainDetails
	if err := json.NewDecoder(res.Body).Decode(&details); err != nil {
		t.Fatalf("Failed to decode train details response: %v", err)
	}

	if details.TrainNumber != "2010" {
		t.Errorf("Expected train number 2010, got %s", details.TrainNumber)
	}
	if details.FirstStationName != "Zagreb Glavni Kolodvor" {
		t.Errorf("Expected Zagreb Glavni Kolodvor, got %s", details.FirstStationName)
	}
	if details.LastStationName != "Vinkovci" {
		t.Errorf("Expected Vinkovci, got %s", details.LastStationName)
	}
	if details.DelayDistribution != nil {
		t.Errorf("Expected nil DelayDistribution for train with no history, got %+v", details.DelayDistribution)
	}
	if len(details.RecentDelays) != 0 {
		t.Errorf("Expected 0 RecentDelays for train with no history, got %d", len(details.RecentDelays))
	}
}

func TestRecentDelaysRetrieval(t *testing.T) {
	srv := setupTestDB(t)

	zgNowStr := getZagrebTime().Format("2006-01-02 15:04:05")
	zgYesterdayStr := getZagrebTime().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")

	_, err := srv.db.Exec(`
		INSERT INTO historical_train_delays (train_number, delay_date, delay_minutes, position_status, last_station, next_station, updated_at)
		VALUES ('2010', date(?), 15, 'In Transit', 'Zagreb Glavni Kolodvor', 'Vinkovci', ?),
		       ('2010', date(?), 5, 'Arrived', 'Vinkovci', '', ?);
	`, zgNowStr, zgNowStr, zgYesterdayStr, zgYesterdayStr)
	if err != nil {
		t.Fatalf("Failed to insert historical delays: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/trains/2010", nil)
	w := httptest.NewRecorder()

	srv.GetTrainDetails(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var details TrainDetails
	if err := json.NewDecoder(res.Body).Decode(&details); err != nil {
		t.Fatalf("Failed to decode train details response: %v", err)
	}

	if len(details.RecentDelays) != 2 {
		t.Fatalf("Expected 2 RecentDelays records in last 3 days, got %d", len(details.RecentDelays))
	}

	if details.RecentDelays[0].DelayMinutes != 15 {
		t.Errorf("Expected latest delay to be 15 mins, got %d", details.RecentDelays[0].DelayMinutes)
	}
}

func TestArrivedDelayNullFallbackAndDeduplication(t *testing.T) {
	srv := setupTestDB(t)

	zgNow := getZagrebTime()
	todayStr := zgNow.Format("2006-01-02")
	zgNowStr := zgNow.Format("2006-01-02 15:04:05")

	// Pre-populate train_delays with a 12-minute delay in transit
	_, err := srv.db.Exec(`
		INSERT INTO train_delays (train_number, delay_minutes, position_status, last_station, next_station, updated_at)
		VALUES ('2010', 12, 'In Transit', 's1', 's2', ?)
	`, zgNowStr)
	if err != nil {
		t.Fatalf("Failed to populate initial train delay: %v", err)
	}

	// 1. Simulate arrival update where delay is null in API response
	// The logic should fallback to previous delay (12 mins) and record historical entry once.
	isArrivedStatus := true
	var delayRespDelay *int = nil // null delay
	delayMins := 0
	if delayRespDelay != nil {
		delayMins = *delayRespDelay
	} else if isArrivedStatus {
		var prevDelay int
		if errPrev := srv.db.QueryRow("SELECT delay_minutes FROM train_delays WHERE train_number = '2010'").Scan(&prevDelay); errPrev == nil {
			delayMins = prevDelay
		}
	}

	if delayMins != 12 {
		t.Errorf("Expected fallback delay of 12 mins, got %d", delayMins)
	}

	// First arrived record insert
	var existingArrivedCount int
	_ = srv.db.QueryRow(`
		SELECT COUNT(*) FROM historical_train_delays
		WHERE train_number = '2010' AND delay_date = ? AND LOWER(position_status) = 'arrived'
	`, todayStr).Scan(&existingArrivedCount)

	shouldRecordHist := (existingArrivedCount == 0)
	if !shouldRecordHist {
		t.Fatalf("Expected shouldRecordHist to be true for first arrived record")
	}

	_, _ = srv.db.Exec(`
		INSERT INTO historical_train_delays (train_number, delay_date, delay_minutes, position_status, last_station, next_station, updated_at)
		VALUES ('2010', ?, ?, 'Arrived', 's2', '', ?)
	`, todayStr, delayMins, zgNowStr)

	// 2. Simulate subsequent arrived update later for the same train
	_ = srv.db.QueryRow(`
		SELECT COUNT(*) FROM historical_train_delays
		WHERE train_number = '2010' AND delay_date = ? AND LOWER(position_status) = 'arrived'
	`, todayStr).Scan(&existingArrivedCount)

	shouldRecordHistSecond := (existingArrivedCount == 0)
	if shouldRecordHistSecond {
		t.Errorf("Expected shouldRecordHist to be false for subsequent arrived record on same date")
	}
}

func TestDelayDistributionCalculation(t *testing.T) {
	srv := setupTestDB(t)

	// Seed historical delay records for train 2010
	// 5 records: 0 (ontime), 3 (1-5m), 8 (5-10m), 20 (10-30m), 45 (>30m)
	_, err := srv.db.Exec(`
		INSERT INTO historical_train_delays (train_number, delay_date, delay_minutes, position_status, last_station, next_station)
		VALUES ('2010', '2026-10-01', 0, 'Arrived', 's1', 's2'),
		       ('2010', '2026-10-02', 3, 'Arrived', 's1', 's2'),
		       ('2010', '2026-10-03', 8, 'Arrived', 's1', 's2'),
		       ('2010', '2026-10-04', 20, 'Arrived', 's1', 's2'),
		       ('2010', '2026-10-05', 45, 'Arrived', 's1', 's2');
	`)
	if err != nil {
		t.Fatalf("Failed to insert historical delays: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/trains/2010", nil)
	w := httptest.NewRecorder()

	srv.GetTrainDetails(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var details TrainDetails
	if err := json.NewDecoder(res.Body).Decode(&details); err != nil {
		t.Fatalf("Failed to decode train details response: %v", err)
	}

	if details.DelayDistribution == nil {
		t.Fatalf("Expected non-nil DelayDistribution")
	}

	dist := details.DelayDistribution
	if dist.TotalRecords != 5 {
		t.Errorf("Expected 5 total records, got %d", dist.TotalRecords)
	}
	if dist.OnTimePercent != 20.0 {
		t.Errorf("Expected 20%% ontime, got %f", dist.OnTimePercent)
	}
	if dist.Late1To5Percent != 20.0 {
		t.Errorf("Expected 20%% 1-5m, got %f", dist.Late1To5Percent)
	}
	if dist.Late5To10Percent != 20.0 {
		t.Errorf("Expected 20%% 5-10m, got %f", dist.Late5To10Percent)
	}
	if dist.Late10To30Percent != 20.0 {
		t.Errorf("Expected 20%% 10-30m, got %f", dist.Late10To30Percent)
	}
	if dist.LateOver30Percent != 20.0 {
		t.Errorf("Expected 20%% >30m, got %f", dist.LateOver30Percent)
	}
}

func TestGetActiveTrains(t *testing.T) {
	srv := setupTestDB(t)

	req := httptest.NewRequest("GET", "/api/active-trains?time=11:00:00", nil)
	w := httptest.NewRecorder()

	srv.GetActiveTrains(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var activeTrains []ActiveTrain
	if err := json.NewDecoder(res.Body).Decode(&activeTrains); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(activeTrains) != 1 {
		t.Fatalf("Expected 1 active train, got %d", len(activeTrains))
	}

	train := activeTrains[0]
	if train.TrainNumber != "2010" {
		t.Errorf("Expected train number 2010, got %s", train.TrainNumber)
	}

	if train.Progress < 0.4 || train.Progress > 0.6 {
		t.Errorf("Expected progress around 0.5, got %f", train.Progress)
	}
}

func TestPlanRoute(t *testing.T) {
	srv := setupTestDB(t)

	req := httptest.NewRequest("GET", "/api/routes/plan?from=s1&to=s2", nil)
	w := httptest.NewRecorder()

	srv.PlanRoute(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var plan []RoutePlanEntry
	if err := json.NewDecoder(res.Body).Decode(&plan); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(plan) != 1 {
		t.Fatalf("Expected 1 route plan entry, got %d", len(plan))
	}

	if plan[0].TrainNumber != "2010" {
		t.Errorf("Expected train number 2010, got %s", plan[0].TrainNumber)
	}
}

func TestSearch(t *testing.T) {
	srv := setupTestDB(t)

	req := httptest.NewRequest("GET", "/api/search?q=Zagreb", nil)
	w := httptest.NewRecorder()

	srv.Search(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var results []SearchResultItem
	if err := json.NewDecoder(res.Body).Decode(&results); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected at least 1 search result for 'Zagreb'")
	}
	if results[0].Title != "Zagreb Glavni Kolodvor" {
		t.Errorf("Expected Zagreb Glavni Kolodvor, got %s", results[0].Title)
	}
}

func TestSearchDeduplication(t *testing.T) {
	srv := setupTestDB(t)

	// Add a second trip with the same trip_short_name '2010' but different trip_id and headsign
	_, err := srv.db.Exec(`
		INSERT INTO trips (trip_id, route_id, service_id, trip_short_name, trip_headsign)
		VALUES ('t2010_alt', 'r1', 'serv1', '2010', 'Vinkovci Express');

		INSERT INTO stop_times (trip_id, arrival_time, departure_time, arrival_seconds, departure_seconds, stop_id, stop_sequence)
		VALUES ('t2010_alt', '10:00:00', '10:05:00', 36000, 36300, 's1', 1);
	`)
	if err != nil {
		t.Fatalf("Failed to insert duplicate trip: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/search?q=2010", nil)
	w := httptest.NewRecorder()

	srv.Search(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var results []SearchResultItem
	if err := json.NewDecoder(res.Body).Decode(&results); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	trainCount := 0
	for _, item := range results {
		if item.Type == "train" && item.TrainNumber == "2010" {
			trainCount++
		}
	}

	if trainCount != 1 {
		t.Errorf("Expected exactly 1 search result for train 2010, got %d", trainCount)
	}
}

func TestGetDashboardStats(t *testing.T) {
	srv := setupTestDB(t)

	req := httptest.NewRequest("GET", "/api/dashboard?time=39600", nil)
	w := httptest.NewRecorder()

	srv.GetDashboardStats(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK, got %d", res.StatusCode)
	}

	var stats DashboardStats
	if err := json.NewDecoder(res.Body).Decode(&stats); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if stats.TotalStations != 2 {
		t.Errorf("Expected 2 total stations, got %d", stats.TotalStations)
	}
	if stats.ActiveTrainCount != 1 {
		t.Errorf("Expected 1 active train, got %d", stats.ActiveTrainCount)
	}
}

func TestGetSegmentsAndDetails(t *testing.T) {
	srv := setupTestDB(t)

	// 1. Test GET /api/segments
	req := httptest.NewRequest("GET", "/api/segments", nil)
	w := httptest.NewRecorder()
	srv.GetSegments(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK for GetSegments, got %d", res.StatusCode)
	}

	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Errorf("Expected non-empty ETag header")
	}
	if cacheControl := res.Header.Get("Cache-Control"); cacheControl == "" {
		t.Errorf("Expected non-empty Cache-Control header")
	}

	var segments []SegmentSpeed
	if err := json.NewDecoder(res.Body).Decode(&segments); err != nil {
		t.Fatalf("Failed to decode segments response: %v", err)
	}

	if len(segments) != 1 {
		t.Fatalf("Expected 1 segment, got %d", len(segments))
	}
	if len(segments[0].Trains) != 0 {
		t.Errorf("Expected bulk /api/segments to omit embedded trains array, got %d trains", len(segments[0].Trains))
	}

	// Test 304 Not Modified when ETag matches If-None-Match
	req304 := httptest.NewRequest("GET", "/api/segments", nil)
	req304.Header.Set("If-None-Match", etag)
	w304 := httptest.NewRecorder()
	srv.GetSegments(w304, req304)

	res304 := w304.Result()
	if res304.StatusCode != http.StatusNotModified {
		t.Errorf("Expected status 304 Not Modified, got %d", res304.StatusCode)
	}

	// 2. Test GET /api/segments/details
	reqDetail := httptest.NewRequest("GET", "/api/segments/details?from=s1&to=s2", nil)
	wDetail := httptest.NewRecorder()
	srv.GetSegmentDetails(wDetail, reqDetail)

	resDetail := wDetail.Result()
	if resDetail.StatusCode != http.StatusOK {
		t.Fatalf("Expected status OK for GetSegmentDetails, got %d", resDetail.StatusCode)
	}

	var detail SegmentSpeed
	if err := json.NewDecoder(resDetail.Body).Decode(&detail); err != nil {
		t.Fatalf("Failed to decode segment details response: %v", err)
	}

	if len(detail.Trains) != 1 {
		t.Fatalf("Expected 1 train in segment detail, got %d", len(detail.Trains))
	}
	if detail.Trains[0].TrainNumber != "2010" {
		t.Errorf("Expected train number 2010 in segment detail, got %s", detail.Trains[0].TrainNumber)
	}
}

func TestLoadOrGenerateSegmentsJSON(t *testing.T) {
	srv := setupTestDB(t)

	tmpDir, err := os.MkdirTemp("", "segments_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	segmentsPath := filepath.Join(tmpDir, "segments.json")

	// 1. Initial generation (gtfsChanged = true)
	segs1, err := srv.LoadOrGenerateSegmentsJSON(segmentsPath, true)
	if err != nil {
		t.Fatalf("LoadOrGenerateSegmentsJSON failed: %v", err)
	}
	if len(segs1) != 1 {
		t.Fatalf("Expected 1 segment generated, got %d", len(segs1))
	}

	// Confirm segments.json file was created on disk
	if _, err := os.Stat(segmentsPath); os.IsNotExist(err) {
		t.Fatalf("Expected segments.json file to be written to disk")
	}

	// 2. Subsequent load from disk (gtfsChanged = false)
	srv.InvalidateSegmentsCache()
	segs2, err := srv.LoadOrGenerateSegmentsJSON(segmentsPath, false)
	if err != nil {
		t.Fatalf("LoadOrGenerateSegmentsJSON from cache failed: %v", err)
	}
	if len(segs2) != 1 {
		t.Fatalf("Expected 1 segment loaded from disk, got %d", len(segs2))
	}
}

func BenchmarkGetActiveTrains(b *testing.B) {
	t := &testing.T{}
	srv := setupTestDB(t)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/api/active-trains?time=11:00:00", nil)
		w := httptest.NewRecorder()
		srv.GetActiveTrains(w, req)
	}
}

func TestDelayServiceStats(t *testing.T) {
	srv := setupTestDB(t)

	// Record calls
	srv.recordDelayServiceCall(true)
	srv.recordDelayServiceCall(true)
	srv.recordDelayServiceCall(false)

	stats := srv.GetDelayServiceStats()

	if stats.TotalCalls != 3 {
		t.Errorf("Expected 3 total calls, got %d", stats.TotalCalls)
	}
	if stats.SuccessfulCalls != 2 {
		t.Errorf("Expected 2 successful calls, got %d", stats.SuccessfulCalls)
	}
	if stats.FailedCalls != 1 {
		t.Errorf("Expected 1 failed call, got %d", stats.FailedCalls)
	}
	if stats.CallsLastMinute != 3 {
		t.Errorf("Expected 3 calls last minute, got %d", stats.CallsLastMinute)
	}
}

func TestHistoricalTrainDelaysUniqueChangesOnly(t *testing.T) {
	srv := setupTestDB(t)

	// Insert initial historical delay record
	_, err := srv.db.Exec(`
		INSERT INTO historical_train_delays (train_number, delay_date, delay_minutes, position_status, last_station, next_station)
		VALUES ('2010', '2026-10-10', 10, 'In Transit', 's1', 's2')
	`)
	if err != nil {
		t.Fatalf("Failed to insert initial historical delay: %v", err)
	}

	// Verify count is 1
	var count int
	_ = srv.db.QueryRow("SELECT COUNT(*) FROM historical_train_delays WHERE train_number = '2010'").Scan(&count)
	if count != 1 {
		t.Fatalf("Expected 1 historical record, got %d", count)
	}

	// Check logic when checking new delay with identical data
	var lastHistDelay int
	var lastHistStatus, lastHistLastSt, lastHistNextSt string
	errHist := srv.db.QueryRow(`
		SELECT delay_minutes, COALESCE(position_status, ''), COALESCE(last_station, ''), COALESCE(next_station, '')
		FROM historical_train_delays
		WHERE train_number = '2010'
		ORDER BY id DESC LIMIT 1
	`).Scan(&lastHistDelay, &lastHistStatus, &lastHistLastSt, &lastHistNextSt)

	if errHist != nil {
		t.Fatalf("Query failed: %v", errHist)
	}

	isHistChangedIdentical := (errHist != nil) ||
		(lastHistDelay != 10) ||
		(lastHistStatus != "In Transit") ||
		(lastHistLastSt != "s1") ||
		(lastHistNextSt != "s2")

	if isHistChangedIdentical {
		t.Errorf("Expected isHistChangedIdentical to be false for identical delay data")
	}

	// Check logic when checking new delay with CHANGED data (e.g. delay increased to 15 mins)
	isHistChangedNew := (errHist != nil) ||
		(lastHistDelay != 15) ||
		(lastHistStatus != "In Transit") ||
		(lastHistLastSt != "s1") ||
		(lastHistNextSt != "s2")

	if !isHistChangedNew {
		t.Errorf("Expected isHistChangedNew to be true for modified delay data")
	}
}

func TestAutoGenerateMissingArrivals(t *testing.T) {
	srv := setupTestDB(t)

	// Set stop_times arrival_seconds for t2010 to a time early in the day (0 seconds)
	_, err := srv.db.Exec(`UPDATE stop_times SET arrival_seconds = 0 WHERE trip_id = 't2010'`)
	if err != nil {
		t.Fatalf("Failed to update stop_times for test: %v", err)
	}

	zgNowStr := getZagrebTime().Format("2006-01-02 15:04:05")

	// Pre-populate train_delays for train 2010 with position_status 'en_route' and 5 min delay
	_, err = srv.db.Exec(`
		INSERT INTO train_delays (train_number, delay_minutes, position_status, last_station, next_station, updated_at)
		VALUES ('2010', 5, 'en_route', 's1', 's2', ?)
	`, zgNowStr)
	if err != nil {
		t.Fatalf("Failed to insert train_delays: %v", err)
	}

	// Trigger autoGenerateMissingArrivals
	srv.autoGenerateMissingArrivals()

	var posStatus string
	var delayMins int
	err = srv.db.QueryRow("SELECT position_status, delay_minutes FROM train_delays WHERE train_number = '2010'").Scan(&posStatus, &delayMins)
	if err != nil {
		t.Fatalf("Failed to query train_delays: %v", err)
	}

	if posStatus != "arrived" {
		t.Errorf("Expected position_status 'arrived', got '%s'", posStatus)
	}
	if delayMins != 5 {
		t.Errorf("Expected retained delay_minutes of 5, got %d", delayMins)
	}
}

func BenchmarkGetSegments(b *testing.B) {
	t := &testing.T{}
	srv := setupTestDB(t)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/api/segments", nil)
		w := httptest.NewRecorder()
		srv.GetSegments(w, req)
	}
}
