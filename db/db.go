package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/mattn/go-sqlite3"
)

var (
	registerOnce sync.Once
	DriverName   = "sqlite3_spatialite"
)

func initDriver() {
	registerOnce.Do(func() {
		sql.Register(DriverName, &sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				entryPoints := []string{"sqlite3_modspatialite_init", "sqlite3_spatialite_init", ""}
				extPaths := []string{
					"mod_spatialite",
					"/usr/lib/x86_64-linux-gnu/mod_spatialite",
				}

				var lastErr error
				for _, ext := range extPaths {
					for _, entry := range entryPoints {
						if err := conn.LoadExtension(ext, entry); err == nil {
							return nil
						} else {
							lastErr = err
						}
					}
				}
				return fmt.Errorf("failed to load SpatiaLite extension: %w", lastErr)
			},
		})
	})
}

func InitDB(dbPath string) (*sql.DB, error) {
	initDriver()

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

	var exists int
	err = database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='spatial_ref_sys'").Scan(&exists)
	if err != nil || exists == 0 {
		log.Println("Initializing SpatiaLite spatial metadata...")
		_, err = database.Exec("SELECT InitSpatialMetaData(1)")
		if err != nil {
			log.Printf("InitSpatialMetaData warning: %v", err)
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
	CREATE INDEX IF NOT EXISTS idx_trips_short_name ON trips(trip_short_name);

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
	`

	if _, err := database.Exec(schema); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	var geomExists int
	err = database.QueryRow("SELECT count(*) FROM geometry_columns WHERE f_table_name='stations' AND f_geometry_column='geom'").Scan(&geomExists)
	if err != nil || geomExists == 0 {
		_, err = database.Exec("SELECT AddGeometryColumn('stations', 'geom', 4326, 'POINT', 'XY')")
		if err != nil {
			log.Printf("AddGeometryColumn stations warning: %v", err)
		}
	}

	return database, nil
}
