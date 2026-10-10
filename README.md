# HŽ Interactive Train Map

An interactive, real-time train map for **Hrvatske Željeznice (HŽ)** built with a **Go** backend, **SQLite** database, and a **Leaflet.js** frontend.

![HŽ Train Map Screenshot](https://raw.githubusercontent.com/placeholder/screenshot.png)

## Features

- **Interactive Map**: Centered on Croatia with customized dark-mode tile rendering and railway network styling.
- **Stations & Timetables**: View all train stations across Croatia and inspect real-time station departure/arrival timetables.
- **Live Train Animation & Interpolation**: Train positions are dynamically interpolated along scheduled routes between consecutive stops according to GTFS timetables and delay adjustments.
- **Real-Time Delays & SSE**: Integrates live delay information from `hzpp.app` API with real-time SSE stream updates.
- **Time Scrubbing & Simulation**: Replay or fast-forward trains at any time of day (with 1x, 5x, 10x, 60x playback speeds).
- **Automated GTFS Ingestion**: Auto-fetches and parses official HŽ GTFS timetable data on startup with SHA-256 checksum caching.

---

## Prerequisites & Installation

### Prerequisites

- **Go 1.20+**
- **GCC / C Compiler** (for `go-sqlite3` CGO compilation)

### macOS (Apple Silicon / Intel)

```bash
brew install go sqlite
```

### Linux (Debian / Ubuntu)

```bash
sudo apt-get update
sudo apt-get install -y golang gcc sqlite3
```

---

## How to Build and Run

1. **Clone the repository**:
   ```bash
   git clone <repository-url>
   cd hz-train-map
   ```

2. **Build the application**:
   ```bash
   go build -o hz-train-map .
   ```

3. **Run the Server**:
   ```bash
   ./hz-train-map
   ```

   *Alternatively, run directly with `go run`:*
   ```bash
   go run main.go
   ```

4. **Access the Web Interface**:
   Open your browser and navigate to:
   ```
   http://localhost:8080
   ```

---

## Architecture & API Overview

### Tech Stack
- **Backend**: Go (`net/http`, `go-sqlite3`)
- **Database**: SQLite (`sqlite3`)
- **Data Source**:
  - GTFS Timetable: `https://www.hzpp.hr/GTFS_files.zip`
  - Live Delays: `https://hzpp.app/api/train-delay?trainId={id}`
  - OSM Railway Track Geometries: Overpass API
- **Frontend**: HTML5, CSS3, JavaScript ES6, Leaflet.js

### API Endpoints

- `GET /api/stations` - Returns all train stations with names and coordinates.
- `GET /api/stations/{id}/timetable` - Returns arrival/departure schedule for a specific station.
- `GET /api/active-trains?time={time}` - Returns real-time or scrubbed active train positions, interpolated between stops.
- `GET /api/segments` - Returns all track segments and average speeds.
- `GET /api/segments/details?from={id}&to={id}` - Returns on-demand train details for a track segment.
- `GET /api/train-delay?trainId={id}` - Proxies and caches live delay query from `hzpp.app`.
- `GET /api/delays/stream` - SSE stream broadcasting real-time delay updates.
- `GET /api/routes/plan?from={id}&to={id}` - Search route itineraries between stations.
- `GET /api/dashboard` - Network-wide active train statistics.

---

## License

MIT
