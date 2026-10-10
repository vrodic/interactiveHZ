package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

const DriverName = "sqlite3"

func InitDB(dbPath string) (*sql.DB, error) {
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create db directory: %w", err)
			}
		}
	}

	database, err := sql.Open(DriverName, dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if dbPath != ":memory:" {
		if _, err := database.Exec("PRAGMA journal_mode=WAL;"); err != nil {
			log.Printf("Warning: failed to set WAL mode: %v", err)
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS stations (
		stop_id TEXT PRIMARY KEY,
		stop_name TEXT NOT NULL,
		stop_lat REAL NOT NULL,
		stop_lon REAL NOT NULL
	);

	CREATE TABLE IF NOT EXISTS routes (
		route_id TEXT PRIMARY KEY,
		agency_id TEXT,
		route_short_name TEXT,
		route_long_name TEXT,
		route_type INTEGER
	);

	CREATE TABLE IF NOT EXISTS trips (
		trip_id TEXT PRIMARY KEY,
		route_id TEXT,
		service_id TEXT,
		trip_headsign TEXT,
		trip_short_name TEXT,
		direction_id INTEGER
	);

	CREATE TABLE IF NOT EXISTS stop_times (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		trip_id TEXT NOT NULL,
		arrival_time TEXT NOT NULL,
		departure_time TEXT NOT NULL,
		arrival_seconds INTEGER NOT NULL,
		departure_seconds INTEGER NOT NULL,
		stop_id TEXT NOT NULL,
		stop_sequence INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_stop_times_trip_id ON stop_times(trip_id);
	CREATE INDEX IF NOT EXISTS idx_stop_times_stop_id ON stop_times(stop_id);
	CREATE INDEX IF NOT EXISTS idx_stop_times_trip_seq ON stop_times(trip_id, stop_sequence);
	CREATE INDEX IF NOT EXISTS idx_stop_times_dep_arr ON stop_times(departure_seconds, arrival_seconds);
	CREATE INDEX IF NOT EXISTS idx_stop_times_dep_arr_trip ON stop_times(departure_seconds, arrival_seconds, trip_id);
	CREATE INDEX IF NOT EXISTS idx_trips_short_name ON trips(trip_short_name);
	CREATE INDEX IF NOT EXISTS idx_trips_service_id ON trips(service_id);

	CREATE TABLE IF NOT EXISTS calendar (
		service_id TEXT PRIMARY KEY,
		start_date TEXT,
		end_date TEXT,
		monday INTEGER,
		tuesday INTEGER,
		wednesday INTEGER,
		thursday INTEGER,
		friday INTEGER,
		saturday INTEGER,
		sunday INTEGER
	);

	CREATE TABLE IF NOT EXISTS train_delays (
		train_number TEXT PRIMARY KEY,
		delay_minutes INTEGER,
		position_status TEXT,
		last_station TEXT,
		next_station TEXT,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS historical_train_delays (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		train_number TEXT NOT NULL,
		delay_date DATE NOT NULL,
		delay_minutes INTEGER NOT NULL,
		position_status TEXT,
		last_station TEXT,
		next_station TEXT,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_hist_delays_date ON historical_train_delays(delay_date);
	CREATE INDEX IF NOT EXISTS idx_hist_delays_train_date ON historical_train_delays(train_number, delay_date);

	CREATE TABLE IF NOT EXISTS route_segments (
		from_stop_id TEXT NOT NULL,
		to_stop_id TEXT NOT NULL,
		train_count INTEGER NOT NULL,
		avg_duration_mins REAL NOT NULL,
		PRIMARY KEY (from_stop_id, to_stop_id)
	);
	`

	if _, err := database.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	var segCount int
	_ = database.QueryRow("SELECT COUNT(*) FROM route_segments").Scan(&segCount)
	if segCount == 0 {
		var stCount int
		_ = database.QueryRow("SELECT COUNT(*) FROM stop_times").Scan(&stCount)
		if stCount > 0 {
			_ = PopulateRouteSegments(database)
		}
	}

	return database, nil
}

func PopulateRouteSegments(database *sql.DB) error {
	query := `
		INSERT INTO route_segments (from_stop_id, to_stop_id, train_count, avg_duration_mins)
		SELECT st1.stop_id, st2.stop_id,
		       COUNT(DISTINCT COALESCE(t.trip_short_name, t.trip_id)) as train_count,
		       COALESCE(AVG(CASE WHEN (st2.arrival_seconds - st1.departure_seconds) > 0 THEN CAST(st2.arrival_seconds - st1.departure_seconds AS REAL) / 60.0 END), 0) as avg_duration_mins
		FROM stop_times st1
		JOIN stop_times st2 ON st1.trip_id = st2.trip_id AND st2.stop_sequence = st1.stop_sequence + 1
		JOIN trips t ON st1.trip_id = t.trip_id
		GROUP BY st1.stop_id, st2.stop_id
		ON CONFLICT(from_stop_id, to_stop_id) DO UPDATE SET
			train_count=excluded.train_count,
			avg_duration_mins=excluded.avg_duration_mins;
	`
	_, err := database.Exec(query)
	return err
}
