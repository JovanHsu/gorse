package main

type AssignResponse struct {
	UserID    string `json:"user_id"`
	Experiment string `json:"experiment"`
	Group     string `json:"group"`
}

type MetricRecord struct {
	Experiment string  `json:"experiment"`
	UserID     string  `json:"user_id"`
	Metric     string  `json:"metric"`
	Value      float64 `json:"value"`
}

type MetricResponse struct {
	Status string `json:"status"`
}

type GroupMetrics struct {
	MatchRate   float64 `json:"match_rate,omitempty"`
	SampleSize  int64   `json:"sample_size"`
	Lift        string  `json:"lift,omitempty"`
}

type ReportResponse struct {
	Experiment string                 `json:"experiment"`
	Groups     map[string]GroupMetrics `json:"groups"`
}
