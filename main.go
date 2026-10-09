package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	server.StartBackgroundDelayWorker()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/stations", server.GetStations)
	mux.HandleFunc("/api/stations/", server.GetStationTimetable)
	mux.HandleFunc("/api/active-trains", server.GetActiveTrains)
	mux.HandleFunc("/api/segments", server.GetSegments)
	mux.HandleFunc("/api/train-delay", server.FetchTrainDelay)
	mux.HandleFunc("/api/routes/plan", server.PlanRoute)
	mux.HandleFunc("/api/dashboard", server.GetDashboardStats)

	staticSubFS, err := fs.Sub(staticEmbedFS, "web/static")
	if err != nil {
		log.Fatalf("Failed to create static sub filesystem: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticSubFS)))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	serverURL := fmt.Sprintf("http://localhost:%s", port)
	log.Printf("HŽ Interactive Train Map server listening on :%s", port)

	go func() {
		time.Sleep(500 * time.Millisecond)
		openBrowser(serverURL)
	}()

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}

func openBrowser(url string) {
	if os.Getenv("NO_BROWSER") != "" {
		return
	}

	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}

	if err != nil {
		log.Printf("Note: Could not open browser automatically (%v). You can access the map at %s", err, url)
	} else {
		log.Printf("Opened %s in default browser", url)
	}
}
