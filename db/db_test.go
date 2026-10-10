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
