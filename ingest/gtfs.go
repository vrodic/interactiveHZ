package ingest

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const DefaultGTFSURL = "https://www.hzpp.hr/GTFS_files.zip"

func IngestGTFS(db *sql.DB, gtfsURL string, localCacheDir string) error {
	log.Println("Starting GTFS ingestion...")

	var zipData []byte
	var err error

	cachedPath := ""
	if localCacheDir != "" {
		cachedPath = filepath.Join(localCacheDir, "GTFS_files.zip")
	}

	useCache := false
	if cachedPath != "" {
		if fi, err := os.Stat(cachedPath); err == nil {
			if time.Since(fi.ModTime()) < 24*time.Hour {
				var count int
				_ = db.QueryRow("SELECT COUNT(*) FROM stations").Scan(&count)
				if count > 0 {
					log.Printf("GTFS cache file %s is less than 24h old and DB is populated (%d stations). Skipping ingestion.", cachedPath, count)
					return nil
				}
			}
		}
	}

	if !useCache && gtfsURL != "" {
		log.Printf("Downloading fresh GTFS data from %s...", gtfsURL)
		zipData, err = downloadGTFS(gtfsURL)
		if err != nil {
			log.Printf("Failed to download GTFS from %s: %v. Checking local cache...", gtfsURL, err)
		} else if localCacheDir != "" {
			_ = os.MkdirAll(localCacheDir, 0755)
			_ = os.WriteFile(cachedPath, zipData, 0644)
		}
	}

	if len(zipData) == 0 && cachedPath != "" {
		if data, err := os.ReadFile(cachedPath); err == nil {
			log.Printf("Loaded GTFS data from local cache: %s", cachedPath)
			zipData = data
		}
	}

	if len(zipData) == 0 {
		return fmt.Errorf("no GTFS data available (download failed and no local cache found)")
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("failed to open GTFS zip reader: %w", err)
	}

	fileMap := make(map[string]*zip.File)
	for _, f := range zipReader.File {
		fileMap[f.Name] = f
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	clearTables := []string{"stop_times", "trips", "routes", "calendar", "stations"}
	for _, tbl := range clearTables {
		if _, err := tx.Exec("DELETE FROM " + tbl); err != nil {
			log.Printf("Warning clearing table %s: %v", tbl, err)
		}
	}

	if f, ok := fileMap["stops.txt"]; ok {
		if err := loadStops(tx, f); err != nil {
			return fmt.Errorf("failed to load stops: %w", err)
		}
	} else {
		return fmt.Errorf("stops.txt missing from GTFS zip")
	}

	if f, ok := fileMap["routes.txt"]; ok {
		if err := loadRoutes(tx, f); err != nil {
			return fmt.Errorf("failed to load routes: %w", err)
		}
	}

	if f, ok := fileMap["trips.txt"]; ok {
		if err := loadTrips(tx, f); err != nil {
			return fmt.Errorf("failed to load trips: %w", err)
		}
	}

	if f, ok := fileMap["calendar.txt"]; ok {
		if err := loadCalendar(tx, f); err != nil {
			return fmt.Errorf("failed to load calendar: %w", err)
		}
	}

	if f, ok := fileMap["stop_times.txt"]; ok {
		if err := loadStopTimes(tx, f); err != nil {
			return fmt.Errorf("failed to load stop_times: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Println("GTFS ingestion completed successfully.")
	return nil
}

func downloadGTFS(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func parseCSVHeader(r *csv.Reader) (map[string]int, error) {
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idxMap := make(map[string]int)
	for i, col := range header {
		idxMap[strings.TrimSpace(col)] = i
	}
	return idxMap, nil
}

func loadStops(tx *sql.Tx, file *zip.File) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	r := csv.NewReader(rc)
	r.FieldsPerRecord = -1
	idxMap, err := parseCSVHeader(r)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO stations (stop_id, stop_name, stop_lat, stop_lon, geom)
		VALUES (?, ?, ?, ?, MakePoint(?, ?, 4326))
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		stopID := record[idxMap["stop_id"]]
		stopName := record[idxMap["stop_name"]]
		latStr := record[idxMap["stop_lat"]]
		lonStr := record[idxMap["stop_lon"]]

		lat, _ := strconv.ParseFloat(latStr, 64)
		lon, _ := strconv.ParseFloat(lonStr, 64)

		_, err = stmt.Exec(stopID, stopName, lat, lon, lon, lat)
		if err != nil {
			log.Printf("Error inserting station %s: %v", stopID, err)
		}
	}
	return nil
}

func loadRoutes(tx *sql.Tx, file *zip.File) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	r := csv.NewReader(rc)
	r.FieldsPerRecord = -1
	idxMap, err := parseCSVHeader(r)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO routes (route_id, agency_id, route_short_name, route_long_name, route_type)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		routeID := record[idxMap["route_id"]]
		agencyID := ""
		if idx, ok := idxMap["agency_id"]; ok && idx < len(record) {
			agencyID = record[idx]
		}
		shortName := ""
		if idx, ok := idxMap["route_short_name"]; ok && idx < len(record) {
			shortName = record[idx]
		}
		longName := ""
		if idx, ok := idxMap["route_long_name"]; ok && idx < len(record) {
			longName = record[idx]
		}
		routeType := 2
		if idx, ok := idxMap["route_type"]; ok && idx < len(record) {
			routeType, _ = strconv.Atoi(record[idx])
		}

		_, _ = stmt.Exec(routeID, agencyID, shortName, longName, routeType)
	}
	return nil
}

func loadTrips(tx *sql.Tx, file *zip.File) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	r := csv.NewReader(rc)
	r.FieldsPerRecord = -1
	idxMap, err := parseCSVHeader(r)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO trips (trip_id, route_id, service_id, trip_headsign, trip_short_name, direction_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		tripID := record[idxMap["trip_id"]]
		routeID := record[idxMap["route_id"]]
		serviceID := record[idxMap["service_id"]]
		headsign := ""
		if idx, ok := idxMap["trip_headsign"]; ok && idx < len(record) {
			headsign = record[idx]
		}
		shortName := ""
		if idx, ok := idxMap["trip_short_name"]; ok && idx < len(record) {
			shortName = record[idx]
		}
		dirID := 0
		if idx, ok := idxMap["direction_id"]; ok && idx < len(record) {
			dirID, _ = strconv.Atoi(record[idx])
		}

		_, _ = stmt.Exec(tripID, routeID, serviceID, headsign, shortName, dirID)
	}
	return nil
}

func loadCalendar(tx *sql.Tx, file *zip.File) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	r := csv.NewReader(rc)
	r.FieldsPerRecord = -1
	idxMap, err := parseCSVHeader(r)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO calendar (service_id, start_date, end_date, monday, tuesday, wednesday, thursday, friday, saturday, sunday)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		serviceID := record[idxMap["service_id"]]
		startDate := record[idxMap["start_date"]]
		endDate := record[idxMap["end_date"]]
		mon, _ := strconv.Atoi(record[idxMap["monday"]])
		tue, _ := strconv.Atoi(record[idxMap["tuesday"]])
		wed, _ := strconv.Atoi(record[idxMap["wednesday"]])
		thu, _ := strconv.Atoi(record[idxMap["thursday"]])
		fri, _ := strconv.Atoi(record[idxMap["friday"]])
		sat, _ := strconv.Atoi(record[idxMap["saturday"]])
		sun, _ := strconv.Atoi(record[idxMap["sunday"]])

		_, _ = stmt.Exec(serviceID, startDate, endDate, mon, tue, wed, thu, fri, sat, sun)
	}
	return nil
}

func loadStopTimes(tx *sql.Tx, file *zip.File) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	r := csv.NewReader(rc)
	r.FieldsPerRecord = -1
	idxMap, err := parseCSVHeader(r)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO stop_times (trip_id, arrival_time, departure_time, arrival_seconds, departure_seconds, stop_id, stop_sequence)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	count := 0
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		tripID := record[idxMap["trip_id"]]
		arrTime := record[idxMap["arrival_time"]]
		depTime := record[idxMap["departure_time"]]
		stopID := record[idxMap["stop_id"]]
		seq, _ := strconv.Atoi(record[idxMap["stop_sequence"]])

		arrSec := TimeToSeconds(arrTime)
		depSec := TimeToSeconds(depTime)

		_, _ = stmt.Exec(tripID, arrTime, depTime, arrSec, depSec, stopID, seq)
		count++
	}
	log.Printf("Loaded %d stop_times records", count)
	return nil
}

func TimeToSeconds(timeStr string) int {
	parts := strings.Split(timeStr, ":")
	if len(parts) < 2 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	s := 0
	if len(parts) >= 3 {
		s, _ = strconv.Atoi(parts[2])
	}
	return h*3600 + m*60 + s
}
