package ingest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type OSMNode struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type OSMWay struct {
	ID       int64     `json:"id"`
	Geometry []OSMNode `json:"geometry"`
}

type OverpassResponse struct {
	Elements []OSMWay `json:"elements"`
}

func FetchAndCacheOSMRailways(db *sql.DB, localCacheDir string) ([]OSMWay, error) {
	var cachedPath string
	if localCacheDir != "" {
		cachedPath = filepath.Join(localCacheDir, "osm_railways.json")
		if fi, err := os.Stat(cachedPath); err == nil && time.Since(fi.ModTime()) < 7*24*time.Hour {
			data, err := os.ReadFile(cachedPath)
			if err == nil {
				var ways []OSMWay
				if err := json.Unmarshal(data, &ways); err == nil && len(ways) > 0 {
					log.Printf("Loaded %d OSM railway ways from local cache %s", len(ways), cachedPath)
					_ = storeOSMWaysInDB(db, ways)
					return ways, nil
				}
			}
		}
	}

	log.Println("Fetching OSM railway tracks for Croatia from Overpass API...")
	query := `[out:json][timeout:30];
(
  way["railway"="rail"](45.0,13.0,46.5,19.5);
);
out geom;`

	req, err := http.NewRequest("POST", "https://overpass-api.de/api/interpreter", bytes.NewBufferString(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "HZTrainMap/1.0")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed Overpass API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Overpass API returned status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading Overpass API response body: %w", err)
	}

	var overpassResp OverpassResponse
	if err := json.Unmarshal(body, &overpassResp); err != nil {
		return nil, fmt.Errorf("failed parsing Overpass API JSON: %w", err)
	}

	ways := make([]OSMWay, 0, len(overpassResp.Elements))
	for _, el := range overpassResp.Elements {
		if len(el.Geometry) >= 2 {
			ways = append(ways, el)
		}
	}

	log.Printf("Successfully fetched %d OSM railway tracks from Overpass API.", len(ways))

	if len(ways) > 0 && localCacheDir != "" {
		_ = os.MkdirAll(localCacheDir, 0755)
		if jsonData, err := json.Marshal(ways); err == nil {
			_ = os.WriteFile(cachedPath, jsonData, 0644)
		}
	}

	_ = storeOSMWaysInDB(db, ways)
	return ways, nil
}

func storeOSMWaysInDB(db *sql.DB, ways []OSMWay) error {
	schema := `
	CREATE TABLE IF NOT EXISTS osm_railways (
		id INTEGER PRIMARY KEY,
		geometry_json TEXT NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.Exec("DELETE FROM osm_railways")

	stmt, err := tx.Prepare("INSERT INTO osm_railways (id, geometry_json) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, w := range ways {
		jsonGeom, err := json.Marshal(w.Geometry)
		if err == nil {
			_, _ = stmt.Exec(w.ID, string(jsonGeom))
		}
	}

	return tx.Commit()
}

func LoadOSMWaysFromDB(db *sql.DB) ([]OSMWay, error) {
	rows, err := db.Query("SELECT id, geometry_json FROM osm_railways")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ways []OSMWay
	for rows.Next() {
		var id int64
		var jsonGeom string
		if err := rows.Scan(&id, &jsonGeom); err == nil {
			var nodes []OSMNode
			if err := json.Unmarshal([]byte(jsonGeom), &nodes); err == nil {
				ways = append(ways, OSMWay{
					ID:       id,
					Geometry: nodes,
				})
			}
		}
	}
	return ways, nil
}
