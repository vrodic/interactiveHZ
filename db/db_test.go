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

	var spatialiteVer string
	err = database.QueryRow("SELECT spatialite_version()").Scan(&spatialiteVer)
	if err != nil {
		t.Fatalf("Failed to get spatialite version: %v", err)
	}

	if spatialiteVer == "" {
		t.Errorf("Expected spatialite version, got empty string")
	}
}
