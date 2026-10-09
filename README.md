# HŽ Interactive Train Map

An interactive, real-time train map for **Hrvatske Željeznice (HŽ)** built with a **Go** backend, **SQLite + SpatiaLite** database, and a **Leaflet.js** frontend.

![HŽ Train Map Screenshot](https://raw.githubusercontent.com/placeholder/screenshot.png)

## Features

- **Interactive Map**: Centered on Croatia with customized dark-mode vector tile rendering and railway network styling.
- **Stations & Timetables**: View all train stations across Croatia and inspect real-time station departure/arrival timetables.
- **Live Train Animation & Interpolation**: Train positions are dynamically interpolated along scheduled routes between consecutive stops according to GTFS timetables and delay adjustments.
- **Real-Time Delays**: Integrates live delay information from `hzpp.app` API for individual trains.
- **Time Scrubbing & Simulation**: Replay or fast-forward trains at any time of day (with 1x, 5x, 10x, 60x playback speeds).
- **Automated GTFS Ingestion**: Auto-fetches and parses official HŽ GTFS timetable data on startup and refreshes it daily.

---

## Prerequisites & Installation

### macOS (Apple Silicon / Intel)

1. **Install Dependencies via Homebrew**:
   ```bash
   brew install go libspatialite sqlite
   ```

2. **Verify Library Installation**:
   Ensure `libspatialite` is installed at `/opt/homebrew/lib/mod_spatialite.dylib` (Apple Silicon) or `/usr/local/lib/mod_spatialite.dylib` (Intel).

### Linux (Debian / Ubuntu)

1. **Install Dependencies**:
   ```bash
   sudo apt-get update
   sudo apt-get install -y golang libspatialite-dev libsqlite3-mod-spatialite
   ```

---

## How to Build and Run

1. **Clone the repository**:
   ```bash
   git clone <repository-url>
   cd hz-train-map
   ```

2. **Build the application with CGO enabled**:
   Because SQLite and SpatiaLite require C library bindings, enable `CGO_ENABLED=1`:

   **On macOS**:
   ```bash
   CGO_ENABLED=1 go build -o hz-train-map .
   ```

   *If macOS gives a library dynamic linking error during build, specify the Homebrew library and header paths:*
   ```bash
   CGO_CFLAGS="-I$(brew --prefix)/include" CGO_LDFLAGS="-L$(brew --prefix)/lib" CGO_ENABLED=1 go build -o hz-train-map .
   ```

   **On Linux**:
   ```bash
   CGO_ENABLED=1 go build -o hz-train-map .
   ```

3. **Run the Server**:
   ```bash
   ./hz-train-map
   ```

   *Alternatively, run directly with `go run`:*
   ```bash
   CGO_ENABLED=1 go run main.go
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
- **Database**: SQLite with SpatiaLite spatial extension (`mod_spatialite`)
- **Data Source**:
  - GTFS Timetable: `https://www.hzpp.hr/GTFS_files.zip`
  - Live Delays: `https://hzpp.app/api/train-delay?trainId={id}`
- **Frontend**: HTML5, CSS3, JavaScript ES6, Leaflet.js

### API Endpoints

- `GET /api/stations` - Returns all train stations with names and spatial coordinates.
- `GET /api/stations/{id}/timetable` - Returns arrival/departure schedule for a specific station.
- `GET /api/active-trains?time={time}` - Returns real-time or scrubbed active train positions, interpolated between stops.
- `GET /api/live-delay?trainId={id}` - Proxies and caches live delay query from `hzpp.app`.

---

## License

MIT
