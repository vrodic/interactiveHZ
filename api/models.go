package api

type Station struct {
	StopID   string  `json:"stop_id"`
	StopName string  `json:"stop_name"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
}

type StationTimetableEntry struct {
	TripID        string `json:"trip_id"`
	TrainNumber   string `json:"train_number"`
	Headsign      string `json:"headsign"`
	ArrivalTime   string `json:"arrival_time"`
	DepartureTime string `json:"departure_time"`
	StopSequence  int    `json:"stop_sequence"`
	DelayMinutes  *int   `json:"delay_minutes,omitempty"`
}

type ActiveTrain struct {
	TripID           string  `json:"trip_id"`
	TrainNumber      string  `json:"train_number"`
	Headsign         string  `json:"headsign"`
	CurrentLat       float64 `json:"lat"`
	CurrentLon       float64 `json:"lon"`
	PrevStationID    string  `json:"prev_station_id"`
	PrevStationName  string  `json:"prev_station_name"`
	NextStationID    string  `json:"next_station_id"`
	NextStationName  string  `json:"next_station_name"`
	Progress         float64 `json:"progress"`
	Status           string  `json:"status"`
	DelayMinutes     int     `json:"delay_minutes"`
	LastStationName  string  `json:"last_station_name,omitempty"`
	PositionStatus   string  `json:"position_status,omitempty"`
	ScheduledDepSec  int     `json:"scheduled_dep_sec"`
	ScheduledArrSec  int     `json:"scheduled_arr_sec"`
}

type DelayAPIResponse struct {
	Success bool `json:"success"`
	Data    struct {
		TrainNumber          int    `json:"trainNumber"`
		From                 string `json:"from"`
		To                   string `json:"to"`
		CurrentLocation      *string`json:"currentLocation"`
		LastStation          string `json:"lastStation"`
		NextStation          *string`json:"nextStation"`
		PositionStatus       string `json:"positionStatus"`
		DelayMinutes         *int   `json:"delayMinutes"`
		IsFinished           bool   `json:"isFinished"`
		UpdatedAt            string `json:"updatedAt"`
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
