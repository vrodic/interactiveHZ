package db

import (
	"testing"
)

func TestInitDB(t *testing.T) {
	database, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	var stationTableCount int
	err = database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='stations'").Scan(&stationTableCount)
	if err != nil {
		t.Fatalf("Failed to query stations table count: %v", err)
	}

	if stationTableCount != 1 {
		t.Errorf("Expected stations table to exist, got count %d", stationTableCount)
	}
}

func TestDedupeHistoricalDelaysKeepsDistinctReadings(t *testing.T) {
	database, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer database.Close()

	_, err = database.Exec(`
		INSERT INTO historical_train_delays (train_number, delay_date, delay_minutes, position_status, last_station, next_station, updated_at) VALUES
		('2019', '2026-10-27', 5, 'running', 'A', 'B', '2026-10-27 16:25:16'),
		('2019', '2026-10-27', 5, 'running', 'A', 'B', '2026-10-27 16:25:16'),
		('2019', '2026-10-27', 5, 'running', 'A', NULL, '2026-10-27 16:30:00'),
		('2019', '2026-10-27', 5, 'running', 'A', NULL, '2026-10-27 16:30:00'),
		('2019', '2026-10-27', 5, 'running', 'A', 'B', '2026-10-27 16:40:00'),
		('2019', '2026-10-27', 7, 'running', 'A', 'B', '2026-10-27 16:25:16')
	`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := DedupeHistoricalDelays(database)
	if err != nil {
		t.Fatalf("dedupe: %v", err)
	}
	if n != 2 {
		t.Errorf("removed %d rows, want 2 exact duplicates", n)
	}
	var left int
	_ = database.QueryRow("SELECT COUNT(*) FROM historical_train_delays").Scan(&left)
	if left != 4 {
		t.Errorf("%d rows left, want 4 distinct readings", left)
	}
}
