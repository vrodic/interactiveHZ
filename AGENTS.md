# Notes for agents and contributors

User-facing setup is in [README.md](README.md). This file covers what you need to change the code.

## Layout

- `main.go` – wiring: DB init, GTFS/OSM ingestion at startup, HTTP routes, static file serving.
- `api/` – HTTP handlers (`handlers.go`), response types (`models.go`), OSM track graph.
- `ingest/` – GTFS download/import, OSM railway fetch, HŽ notices scraping.
- `db/` – SQLite schema (`InitDB`).
- `web/static/` – the whole frontend: `app.js` (Leaflet map, panels, trip planner), `i18n.js`, `style.css`, `sw.js`.
- Root `*.py` / `*.sql` – one-off data repair scripts, not part of the app.

## Build, test, run

```bash
go build ./... && go test ./...
```

- `web/static/` is embedded into the binary (`//go:embed`), so rebuild after any frontend change.
- Handler tests use `setupTestDB` in `api/handlers_test.go` (in-memory SQLite with a seeded train `2010`). Add a test there for new endpoints.
- `node --check web/static/app.js` is a quick syntax check for the frontend.
- Several Go files are not `gofmt`-clean already; don't reformat whole files as part of unrelated changes.

## Running without network access

At startup the server downloads GTFS from hzpp.hr, track geometry from the Overpass API and notices from hzpp.hr. In a sandbox these fail and the map is empty. Options:

- Put a `GTFS_files.zip` in the DB directory (`./data/` by default, `DB_PATH` sets the DB file); it's used as a local cache when the download fails.
- Or start the server once to create `data/hz_trains.db`, stop it, insert a few rows into `stations`, `routes`, `calendar`, `trips` and `stop_times` (see `setupTestDB` for the shape), and start it again.

Without OSM data, trains move along straight lines between stations. Leaflet is loaded from `unpkg.com`; for headless browser checks, allow it or serve a local copy (e.g. Playwright `page.route`).

## Domain rules

- All times are Europe/Zagreb (`getZagrebTime()`); GTFS times are seconds since the service day's midnight.
- Trains running past midnight belong to the previous day's service with times beyond 24:00. `ComputeActiveTrains` handles this by also querying yesterday's calendar at `secondsOfDay + 86400`.
- Delays come from the `train_delays` table and are ignored when older than 6 hours.
- Whether a train is running, and where it is, must come from `ComputeActiveTrains`. Don't write a separate query for it: the details panel and the map markers must agree (the locate button depends on it).
- Trip planner transfers have a combined train number like `2201 ➔ 2305`; code taking a train number from the planner should handle both legs.

## Frontend conventions

- All user-visible text goes through `t('English text', { vars })`, with the Croatian translation added to `I18N_HR` in `i18n.js`. Croatian inflects station names, so prefer `Label: {station}` over building sentences around a name.
- Escape anything from the API with `escapeHTML` before putting it in HTML.
- Overlays (side panel, modals, planner) are tied to URL hashes (`#train/2702`, `#planner/<from>/<to>`, ...) through `pushOverlayState` / `applyHashRoute`.
- Recent trip planner searches are in `localStorage` under `hz_recent_pairs` (newest first).
