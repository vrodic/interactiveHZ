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

type NoticeResponse struct {
	ID               string   `json:"id"`
	Category         string   `json:"category"`
	Title            string   `json:"title"`
	Content          string   `json:"content"`
	ParsedStations   []string `json:"parsed_stations"`
	ParsedTrains     []string `json:"parsed_trains"`
	PDFURLs          []string `json:"pdf_urls"`
	IsBusReplacement bool     `json:"is_bus_replacement"`
	IsTrackWorks     bool     `json:"is_track_works"`
	DateFrom         string   `json:"date_from,omitempty"`
	DateTo           string   `json:"date_to,omitempty"`
	DatesJSON        []string `json:"dates_json,omitempty"`
	UpdatedAt        string   `json:"updated_at"`
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
	IsBusReplacement       bool   `json:"is_bus_replacement,omitempty"`
	IsTrackWorks           bool   `json:"is_track_works,omitempty"`
	NoticeID               string `json:"notice_id,omitempty"`
}

type RoutePlanEntry struct {
	TripID                 string `json:"trip_id"`
	TrainNumber            string `json:"train_number"`
	Headsign               string `json:"headsign"`
	OriginStationName      string `json:"origin_station_name"`
	DestinationStationName string `json:"destination_station_name"`
	DepartureTime          string `json:"departure_time"`
	ArrivalTime            string `json:"arrival_time"`
	DepartureSeconds       int    `json:"departure_seconds"`
	DurationMinutes        int    `json:"duration_minutes"`
	DelayMinutes           int    `json:"delay_minutes"`
	IsNearestFuture        bool   `json:"is_nearest_future,omitempty"`
	IsTransfer             bool   `json:"is_transfer,omitempty"`
	TransferStationName    string `json:"transfer_station_name,omitempty"`
	AffectedByDisruption   bool   `json:"affected_by_disruption,omitempty"`
	DisruptionNotice       string `json:"disruption_notice,omitempty"`
	IsBusReplacement       bool   `json:"is_bus_replacement,omitempty"`
	IsTrackWorks           bool   `json:"is_track_works,omitempty"`
	NoticeID               string `json:"notice_id,omitempty"`
}

type DelayServiceStats struct {
	TotalCalls        int64  `json:"total_calls"`
	SuccessfulCalls   int64  `json:"successful_calls"`
	FailedCalls       int64  `json:"failed_calls"`
	CallsLastMinute   int    `json:"calls_last_minute"`
	CallsLastHour     int    `json:"calls_last_hour"`
	LastCallTime      string `json:"last_call_time,omitempty"`
}

type DashboardStats struct {
	ActiveTrainCount    int                `json:"active_train_count"`
	DelayedTrainCount   int                `json:"delayed_train_count"`
	OnTimeTrainCount    int                `json:"ontime_train_count"`
	AvgDelayMinutes     float64            `json:"avg_delay_minutes"`
	MaxDelayMinutes     int                `json:"max_delay_minutes"`
	TotalStations       int                `json:"total_stations"`
	TripsRunningToday   int                `json:"trips_running_today"`
	RecentDelayedTrains []ActiveTrain      `json:"recent_delayed_trains"`
	DelayServiceStats   *DelayServiceStats `json:"delay_service_stats,omitempty"`
}

type SearchResultItem struct {
	Type        string  `json:"type"` // "station" or "train"
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Subtitle    string  `json:"subtitle"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	TrainNumber string  `json:"train_number,omitempty"`
}

type ActiveTrain struct {
	TripID           string   `json:"trip_id"`
	TrainNumber      string   `json:"train_number"`
	Headsign         string   `json:"headsign"`
	CurrentLat       float64  `json:"lat"`
	CurrentLon       float64  `json:"lon"`
	FirstStationName string   `json:"first_station_name"`
	LastStationName  string   `json:"last_station_name"`
	PrevStationID    string   `json:"prev_station_id"`
	PrevStationName  string   `json:"prev_station_name"`
	NextStationID    string   `json:"next_station_id"`
	NextStationName  string   `json:"next_station_name"`
	SegmentID        string   `json:"segment_id"`
	Progress         float64  `json:"progress"`
	Status           string   `json:"status"`
	DelayMinutes     int      `json:"delay_minutes"`
	PositionStatus   string   `json:"position_status,omitempty"`
	ScheduledDepSec  int      `json:"scheduled_dep_sec"`
	ScheduledArrSec  int      `json:"scheduled_arr_sec"`
	IsBusReplacement bool     `json:"is_bus_replacement,omitempty"`
	IsTrackWorks     bool     `json:"is_track_works,omitempty"`
	NoticeID         string   `json:"notice_id,omitempty"`
	Path             []LatLon `json:"path,omitempty"`
}

type DelayDistribution struct {
	TotalRecords      int     `json:"total_records"`
	OnTimePercent     float64 `json:"ontime_percent"`
	Late1To5Percent   float64 `json:"late_1_to_5_percent"`
	Late5To10Percent  float64 `json:"late_5_to_10_percent"`
	Late10To30Percent float64 `json:"late_10_to_30_percent"`
	LateOver30Percent float64 `json:"late_over_30_percent"`
}

type RecentDelayLog struct {
	DelayDate      string `json:"delay_date"`
	DelayMinutes   int    `json:"delay_minutes"`
	PositionStatus string `json:"position_status,omitempty"`
	LastStation    string `json:"last_station,omitempty"`
	UpdatedAt      string `json:"updated_at"`
}

type TrainDetails struct {
	TrainNumber           string             `json:"train_number"`
	TripID                string             `json:"trip_id"`
	Headsign              string             `json:"headsign"`
	FirstStationName      string             `json:"first_station_name"`
	LastStationName       string             `json:"last_station_name"`
	FirstDepartureTime    string             `json:"first_departure_time"`
	LastArrivalTime       string             `json:"last_arrival_time"`
	FirstDepartureSeconds int                `json:"first_departure_seconds"`
	LastArrivalSeconds    int                `json:"last_arrival_seconds"`
	DelayMinutes          int                `json:"delay_minutes"`
	PositionStatus        string             `json:"position_status,omitempty"`
	IsActive              bool               `json:"is_active"`
	IsBusReplacement      bool               `json:"is_bus_replacement,omitempty"`
	IsTrackWorks          bool               `json:"is_track_works,omitempty"`
	NoticeID              string             `json:"notice_id,omitempty"`
	Notices               []NoticeResponse   `json:"notices,omitempty"`
	Lat                   float64            `json:"lat,omitempty"`
	Lon                   float64            `json:"lon,omitempty"`
	DelayDistribution     *DelayDistribution `json:"delay_distribution,omitempty"`
	RecentDelays          []RecentDelayLog   `json:"recent_delays,omitempty"`
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
	ID           string              `json:"id"`
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
	Path         []LatLon            `json:"path,omitempty"`
	Trains       []TrainSegmentSpeed `json:"trains,omitempty"`
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
