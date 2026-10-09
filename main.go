package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"hz-train-map/api"
	"hz-train-map/db"
	"hz-train-map/ingest"
)

//go:embed web/static/*
var staticEmbedFS embed.FS

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./data/hz_trains.db"
	}

	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	go func() {
		localCacheDir := filepath.Dir(dbPath)
		gtfsURL := ingest.DefaultGTFSURL

		log.Println("Running initial GTFS data ingestion in background...")
		if err := ingest.IngestGTFS(database, gtfsURL, localCacheDir); err != nil {
			log.Printf("Initial GTFS ingestion warning: %v", err)
		}

		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			log.Println("Periodic 24h ticker: updating GTFS data...")
			if err := ingest.IngestGTFS(database, gtfsURL, localCacheDir); err != nil {
				log.Printf("Periodic GTFS ingestion failed: %v", err)
			}
		}
	}()

	server := api.NewServer(database)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/stations", server.GetStations)
	mux.HandleFunc("/api/stations/", server.GetStationTimetable)
	mux.HandleFunc("/api/active-trains", server.GetActiveTrains)
	mux.HandleFunc("/api/train-delay", server.FetchTrainDelay)

	staticSubFS, err := fs.Sub(staticEmbedFS, "web/static")
	if err != nil {
		log.Fatalf("Failed to create static sub filesystem: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticSubFS)))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("HŽ Interactive Train Map server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}
