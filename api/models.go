package api

type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Station struct {
	StopID   string  `json:"stop_id"`
	StopName string  `json:"stop_name"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
}

type StationTimetableEntry struct {
	TripID                 string `json:"trip_id"`
	TrainNumber            string `json:"train_number"`
	Headsign               string `json:"headsign"`
	DestinationStationName string `json:"destination_station_name"`
	ArrivalTime            string `json:"arrival_time"`
	DepartureTime          string `json:"departure_time"`
	StopSequence           int    `json:"stop_sequence"`
	DelayMinutes           *int   `json:"delay_minutes,omitempty"`
}

type RoutePlanEntry struct {
	TripID                 string `json:"trip_id"`
	TrainNumber            string `json:"train_number"`
	Headsign               string `json:"headsign"`
	OriginStationName      string `json:"origin_station_name"`
	DestinationStationName string `json:"destination_station_name"`
	DepartureTime          string `json:"departure_time"`
	ArrivalTime            string `json:"arrival_time"`
	DurationMinutes        int    `json:"duration_minutes"`
	DelayMinutes           int    `json:"delay_minutes"`
}

type DashboardStats struct {
	ActiveTrainCount    int           `json:"active_train_count"`
	DelayedTrainCount   int           `json:"delayed_train_count"`
	OnTimeTrainCount    int           `json:"ontime_train_count"`
	AvgDelayMinutes     float64       `json:"avg_delay_minutes"`
	MaxDelayMinutes     int           `json:"max_delay_minutes"`
	TotalStations       int           `json:"total_stations"`
	TotalTripsToday     int           `json:"total_trips_today"`
	RecentDelayedTrains []ActiveTrain `json:"recent_delayed_trains"`
}

type ActiveTrain struct {
	TripID           string  `json:"trip_id"`
	TrainNumber      string  `json:"train_number"`
	Headsign         string  `json:"headsign"`
	CurrentLat       float64 `json:"lat"`
	CurrentLon       float64 `json:"lon"`
	FirstStationName string  `json:"first_station_name"`
	LastStationName  string  `json:"last_station_name"`
	PrevStationID    string  `json:"prev_station_id"`
	PrevStationName  string  `json:"prev_station_name"`
	NextStationID    string  `json:"next_station_id"`
	NextStationName  string  `json:"next_station_name"`
	Progress         float64 `json:"progress"`
	Status           string  `json:"status"`
	DelayMinutes     int     `json:"delay_minutes"`
	PositionStatus   string  `json:"position_status,omitempty"`
	ScheduledDepSec  int     `json:"scheduled_dep_sec"`
	ScheduledArrSec  int     `json:"scheduled_arr_sec"`
	Path             []LatLon `json:"path,omitempty"`
}

type TrainSegmentSpeed struct {
	TrainNumber     string  `json:"train_number"`
	TripID          string  `json:"trip_id"`
	Headsign        string  `json:"headsign"`
	ScheduledDepSec int     `json:"scheduled_dep_sec"`
	ScheduledArrSec int     `json:"scheduled_arr_sec"`
	DurationMinutes float64 `json:"duration_minutes"`
	SpeedKmh        float64 `json:"speed_kmh"`
}

type SegmentSpeed struct {
	FromStopID   string              `json:"from_stop_id"`
	FromStopName string              `json:"from_stop_name"`
	FromLat      float64             `json:"from_lat"`
	FromLon      float64             `json:"from_lon"`
	ToStopID     string              `json:"to_stop_id"`
	ToStopName   string              `json:"to_stop_name"`
	ToLat        float64             `json:"to_lat"`
	ToLon        float64             `json:"to_lon"`
	DistanceKm   float64             `json:"distance_km"`
	AvgSpeedKmh  float64             `json:"avg_speed_kmh"`
	TrainCount   int                 `json:"train_count"`
	Trains       []TrainSegmentSpeed `json:"trains"`
}

type DelayAPIResponse struct {
	Success bool `json:"success"`
	Data    struct {
		TrainNumber     int     `json:"trainNumber"`
		From            string  `json:"from"`
		To              string  `json:"to"`
		CurrentLocation *string `json:"currentLocation"`
		LastStation     string  `json:"lastStation"`
		NextStation     *string `json:"nextStation"`
		PositionStatus  string  `json:"positionStatus"`
		DelayMinutes    *int    `json:"delayMinutes"`
		IsFinished      bool    `json:"isFinished"`
		UpdatedAt       string  `json:"updatedAt"`
	} `json:"data"`
	Map struct {
		Stops []struct {
			Name  string  `json:"name"`
			Lat   float64 `json:"lat"`
			Lon   float64 `json:"lon"`
			Index int     `json:"index"`
		} `json:"stops"`
		Position *struct {
			Lat float64 `json:"lat"`
			Lon float64 `json:"lon"`
		} `json:"position"`
	} `json:"map"`
}
