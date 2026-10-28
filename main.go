package main

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"

	_ "time/tzdata"
	"io"
	"io/fs"
	"log"
	"mime"
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

// staticHandler serves the embedded files with a content-hash ETag. Embedded files have
// no modification time, so without it browsers could keep a stale app.js after an update.
func staticHandler(fsys fs.FS) http.Handler {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")

	etags := map[string]string{}
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		etags["/"+path] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	etags["/"] = etags["/index.html"]

	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if etag, ok := etags[r.URL.Path]; ok {
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

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
	gtfsChanged, err := ingest.IngestGTFS(database, gtfsURL, localCacheDir)
	if err != nil {
		log.Printf("Initial GTFS ingestion warning: %v", err)
	}

	if osmWays, err := ingest.FetchAndCacheOSMRailways(database, localCacheDir); err == nil && len(osmWays) > 0 {
		graph := api.BuildOSMGraph(osmWays)
		server.SetOSMGraph(graph)
		log.Printf("OSM railway graph built with %d track segments.", len(osmWays))
	} else {
		log.Printf("OSM railway ingestion warning: %v", err)
	}

	segmentsJSONPath := filepath.Join(localCacheDir, "segments.json")
	segs, err := server.LoadOrGenerateSegmentsJSON(segmentsJSONPath, gtfsChanged)
	if err != nil {
		log.Printf("Error loading or generating segments.json: %v", err)
	} else {
		log.Printf("Track segment cache ready with %d segments.", len(segs))
	}

	// Background ticker for periodic 24h GTFS updates
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			log.Println("Periodic 24h ticker: updating GTFS data...")
			if changed, err := ingest.IngestGTFS(database, gtfsURL, localCacheDir); err != nil {
				log.Printf("Periodic GTFS ingestion failed: %v", err)
			} else if changed {
				log.Println("GTFS data changed, regenerating segments.json...")
				_, _ = server.LoadOrGenerateSegmentsJSON(segmentsJSONPath, true)
			}
		}
	}()

	server.StartBackgroundDelayWorker()
	ingest.StartNoticesBackgroundWorker(database, func() []string {
		rows, err := database.Query("SELECT stop_name FROM stations")
		if err != nil {
			return nil
		}
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				names = append(names, name)
			}
		}
		return names
	})

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	mux.HandleFunc("/api/search", server.Search)
	mux.HandleFunc("/api/notices", server.GetNotices)
	mux.HandleFunc("/api/stations", server.GetStations)
	mux.HandleFunc("/api/stations/", server.GetStationTimetable)
	mux.HandleFunc("/api/trains/", server.GetTrainDetails)
	mux.HandleFunc("/api/train-stop", server.GetTrainStopTime)
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
	mux.Handle("/", staticHandler(staticSubFS))

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
