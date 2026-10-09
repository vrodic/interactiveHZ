package ingest

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"hz-train-map/db"
)

func createTestGTFSZip(t *testing.T) []byte {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	files := map[string]string{
		"stops.txt": `stop_id,stop_name,stop_lat,stop_lon
s1,Zagreb Glavni Kolodvor,45.8044,15.9788
s2,Vinkovci,45.3004,18.8028`,
		"routes.txt": `route_id,agency_id,route_short_name,route_long_name,route_type
r1,HŽ,,Zagreb - Vinkovci,2`,
		"trips.txt": `route_id,service_id,trip_id,trip_headsign,trip_short_name,direction_id
r1,serv1,t2010,,2010,0`,
		"calendar.txt": `service_id,start_date,end_date,monday,tuesday,wednesday,thursday,friday,saturday,sunday
serv1,20260101,20261231,1,1,1,1,1,1,1`,
		"stop_times.txt": `trip_id,arrival_time,departure_time,stop_id,stop_sequence
t2010,08:00:00,08:05:00,s1,1
t2010,11:30:00,11:30:00,s2,2`,
	}

	for name, content := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Failed to create file in zip: %v", err)
		}
		_, err = f.Write([]byte(content))
		if err != nil {
			t.Fatalf("Failed to write content to zip: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("Failed to close zip writer: %v", err)
	}

	return buf.Bytes()
}

func TestIngestGTFS(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}
	defer database.Close()

	tmpDir, err := os.MkdirTemp("", "gtfs_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	zipData := createTestGTFSZip(t)
	zipPath := filepath.Join(tmpDir, "GTFS_files.zip")
	if err := os.WriteFile(zipPath, zipData, 0644); err != nil {
		t.Fatalf("Failed to write test zip: %v", err)
	}

	err = IngestGTFS(database, "", tmpDir)
	if err != nil {
		t.Fatalf("IngestGTFS failed: %v", err)
	}

	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM stations").Scan(&count)
	if err != nil || count != 2 {
		t.Errorf("Expected 2 stations, got %d (err: %v)", count, err)
	}

	err = database.QueryRow("SELECT COUNT(*) FROM trips").Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("Expected 1 trip, got %d (err: %v)", count, err)
	}

	err = database.QueryRow("SELECT COUNT(*) FROM stop_times").Scan(&count)
	if err != nil || count != 2 {
		t.Errorf("Expected 2 stop_times, got %d (err: %v)", count, err)
	}
}

func TestTimeToSeconds(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"00:00:00", 0},
		{"08:30:15", 8*3600 + 30*60 + 15},
		{"24:15:00", 24*3600 + 15*60},
	}

	for _, tt := range tests {
		got := TimeToSeconds(tt.input)
		if got != tt.expected {
			t.Errorf("TimeToSeconds(%s) = %d; want %d", tt.input, got, tt.expected)
		}
	}
}
