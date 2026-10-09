package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	db         *sql.DB
	delayCache sync.Map
}

func NewServer(db *sql.DB) *Server {
	return &Server{
		db: db,
	}
}

func (s *Server) GetStations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`
		SELECT stop_id, stop_name, stop_lat, stop_lon
		FROM stations
		ORDER BY stop_name ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	stations := make([]Station, 0)
	for rows.Next() {
		var st Station
		if err := rows.Scan(&st.StopID, &st.StopName, &st.Lat, &st.Lon); err != nil {
			continue
		}
		stations = append(stations, st)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stations)
}

func (s *Server) GetStationTimetable(w http.ResponseWriter, r *http.Request) {
	stopID := strings.TrimPrefix(r.URL.Path, "/api/stations/")
	stopID = strings.TrimSuffix(stopID, "/timetable")

	if stopID == "" {
		http.Error(w, "Station ID required", http.StatusBadRequest)
		return
	}

	query := `
		SELECT st.trip_id, COALESCE(t.trip_short_name, t.trip_id), COALESCE(t.trip_headsign, ''),
		       st.arrival_time, st.departure_time, st.stop_sequence
		FROM stop_times st
		JOIN trips t ON st.trip_id = t.trip_id
		WHERE st.stop_id = ?
		ORDER BY st.departure_seconds ASC
		LIMIT 100
	`

	rows, err := s.db.Query(query, stopID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	entries := make([]StationTimetableEntry, 0)
	for rows.Next() {
		var entry StationTimetableEntry
		if err := rows.Scan(&entry.TripID, &entry.TrainNumber, &entry.Headsign, &entry.ArrivalTime, &entry.DepartureTime, &entry.StopSequence); err != nil {
			continue
		}

		var delayMins int
		err := s.db.QueryRow("SELECT delay_minutes FROM train_delays WHERE train_number = ?", entry.TrainNumber).Scan(&delayMins)
		if err == nil {
			entry.DelayMinutes = &delayMins
		}

		entries = append(entries, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (s *Server) GetActiveTrains(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	secondsOfDay := now.Hour()*3600 + now.Minute()*60 + now.Second()

	if tsStr := r.URL.Query().Get("time"); tsStr != "" {
		if sec, err := strconv.Atoi(tsStr); err == nil {
			secondsOfDay = sec
		} else if parts := strings.Split(tsStr, ":"); len(parts) >= 2 {
			h, _ := strconv.Atoi(parts[0])
			m, _ := strconv.Atoi(parts[1])
			sec := 0
			if len(parts) >= 3 {
				sec, _ = strconv.Atoi(parts[2])
			}
			secondsOfDay = h*3600 + m*60 + sec
		}
	}

	// Pre-load train delays into memory to avoid N+1 DB queries per row
	delayMap := make(map[string]int)
	if delayRows, err := s.db.Query("SELECT train_number, delay_minutes FROM train_delays"); err == nil {
		defer delayRows.Close()
		for delayRows.Next() {
			var trNum string
			var delMins int
			if err := delayRows.Scan(&trNum, &delMins); err == nil {
				delayMap[trNum] = delMins
			}
		}
	}

	// Filter stop times by time window to drastically reduce scanned rows
	// Allow for delays up to 180 minutes (10800s) and early departures up to 30 minutes (1800s)
	windowStart := secondsOfDay - 10800
	windowEnd := secondsOfDay + 1800

	query := `
		SELECT t.trip_id, COALESCE(t.trip_short_name, t.trip_id), COALESCE(t.trip_headsign, ''),
		       st1.stop_id, s1.stop_name, s1.stop_lat, s1.stop_lon, st1.departure_seconds,
		       st2.stop_id, s2.stop_name, s2.stop_lat, s2.stop_lon, st2.arrival_seconds
		FROM stop_times st1
		JOIN stop_times st2 ON st1.trip_id = st2.trip_id AND st2.stop_sequence = st1.stop_sequence + 1
		JOIN stations s1 ON st1.stop_id = s1.stop_id
		JOIN stations s2 ON st2.stop_id = s2.stop_id
		JOIN trips t ON st1.trip_id = t.trip_id
		WHERE st1.departure_seconds <= ? AND st2.arrival_seconds >= ?
	`

	rows, err := s.db.Query(query, windowEnd, windowStart)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	activeTrainsMap := make(map[string]ActiveTrain)

	for rows.Next() {
		var (
			tripID, trainNum, headsign              string
			s1ID, s1Name                            string
			s1Lat, s1Lon                            float64
			s1DepSec                                int
			s2ID, s2Name                            string
			s2Lat, s2Lon                            float64
			s2ArrSec                                int
		)

		if err := rows.Scan(&tripID, &trainNum, &headsign, &s1ID, &s1Name, &s1Lat, &s1Lon, &s1DepSec, &s2ID, &s2Name, &s2Lat, &s2Lon, &s2ArrSec); err != nil {
			continue
		}

		delayMinutes := delayMap[trainNum]

		effectiveTime := secondsOfDay - (delayMinutes * 60)

		if effectiveTime >= s1DepSec && effectiveTime <= s2ArrSec {
			duration := float64(s2ArrSec - s1DepSec)
			progress := 0.0
			status := "running"

			if duration > 0 {
				progress = float64(effectiveTime-s1DepSec) / duration
				if progress < 0 {
					progress = 0
				}
				if progress > 1 {
					progress = 1
				}
			} else {
				status = "at_station"
			}

			curLat := s1Lat + (s2Lat-s1Lat)*progress
			curLon := s1Lon + (s2Lon-s1Lon)*progress

			activeTrainsMap[tripID] = ActiveTrain{
				TripID:          tripID,
				TrainNumber:     trainNum,
				Headsign:        headsign,
				CurrentLat:      curLat,
				CurrentLon:      curLon,
				PrevStationID:   s1ID,
				PrevStationName: s1Name,
				NextStationID:   s2ID,
				NextStationName: s2Name,
				Progress:        progress,
				Status:          status,
				DelayMinutes:    delayMinutes,
				ScheduledDepSec: s1DepSec,
				ScheduledArrSec: s2ArrSec,
			}
		}
	}

	activeTrains := make([]ActiveTrain, 0, len(activeTrainsMap))
	for _, train := range activeTrainsMap {
		activeTrains = append(activeTrains, train)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(activeTrains)
}

func (s *Server) FetchTrainDelay(w http.ResponseWriter, r *http.Request) {
	trainID := r.URL.Query().Get("trainId")
	if trainID == "" {
		http.Error(w, "trainId parameter is required", http.StatusBadRequest)
		return
	}

	url := fmt.Sprintf("https://hzpp.app/api/train-delay?trainId=%s", trainID)
	client := &http.Client{Timeout: 8 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Error fetching train delay from hzpp.app for train %s: %v", trainID, err)
		if cached, ok := s.delayCache.Load(trainID); ok {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cached)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to fetch live delay: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var delayResp DelayAPIResponse
	if err := json.Unmarshal(body, &delayResp); err == nil && delayResp.Success {
		s.delayCache.Store(trainID, delayResp)

		delayMins := 0
		if delayResp.Data.DelayMinutes != nil {
			delayMins = *delayResp.Data.DelayMinutes
		}
		nextSt := ""
		if delayResp.Data.NextStation != nil {
			nextSt = *delayResp.Data.NextStation
		}

		_, _ = s.db.Exec(`
			INSERT INTO train_delays (train_number, delay_minutes, position_status, last_station, next_station, updated_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(train_number) DO UPDATE SET
				delay_minutes=excluded.delay_minutes,
				position_status=excluded.position_status,
				last_station=excluded.last_station,
				next_station=excluded.next_station,
				updated_at=CURRENT_TIMESTAMP
		`, trainID, delayMins, delayResp.Data.PositionStatus, delayResp.Data.LastStation, nextSt)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
