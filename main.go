package main

import (
	"compress/gzip"
	"context"
	"embed"
	"fmt"

	_ "time/tzdata"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"hz-train-map/api"
	"hz-train-map/db"
	"hz-train-map/ingest"
)

//go:embed web/static/*
var staticEmbedFS embed.FS

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || strings.Contains(r.URL.Path, "/api/delays/stream") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next.ServeHTTP(gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

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

	server := api.NewServer(database)

	localCacheDir := filepath.Dir(dbPath)
	gtfsURL := ingest.DefaultGTFSURL

	log.Println("Preparing GTFS data and warming track segment cache...")
	if err := ingest.IngestGTFS(database, gtfsURL, localCacheDir); err != nil {
		log.Printf("Initial GTFS ingestion warning: %v", err)
	}

	if osmWays, err := ingest.FetchAndCacheOSMRailways(database, localCacheDir); err == nil && len(osmWays) > 0 {
		graph := api.BuildOSMGraph(osmWays)
		server.SetOSMGraph(graph)
		log.Printf("OSM railway graph built with %d track segments.", len(osmWays))
	} else {
		log.Printf("OSM railway ingestion warning: %v", err)
	}

	// Warm track segment cache synchronously so /api/segments is 100% pre-built in RAM before browser launches
	segs := server.GetCachedSegments()
	log.Printf("Track segment cache pre-warmed with %d segments.", len(segs))

	// Background ticker for periodic 24h GTFS updates
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			log.Println("Periodic 24h ticker: updating GTFS data...")
			if err := ingest.IngestGTFS(database, gtfsURL, localCacheDir); err != nil {
				log.Printf("Periodic GTFS ingestion failed: %v", err)
			}
		}
	}()

	server.StartBackgroundDelayWorker()

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	mux.HandleFunc("/api/search", server.Search)
	mux.HandleFunc("/api/stations", server.GetStations)
	mux.HandleFunc("/api/stations/", server.GetStationTimetable)
	mux.HandleFunc("/api/active-trains", server.GetActiveTrains)
	mux.HandleFunc("/api/segments", server.GetSegments)
	mux.HandleFunc("/api/segments/details", server.GetSegmentDetails)
	mux.HandleFunc("/api/train-delay", server.FetchTrainDelay)
	mux.HandleFunc("/api/delays/stream", server.StreamDelays)
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

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      gzipMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("HŽ Interactive Train Map server listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server ListenAndServe error: %v", err)
		}
	}()

	go func() {
		time.Sleep(500 * time.Millisecond)
		openBrowser(serverURL)
	}()

	<-stopChan
	log.Println("Shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("Server stopped.")
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
