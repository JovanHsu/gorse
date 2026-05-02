package main

import "time"

type HealthResponse struct {
	Status string `json:"status"`
}

type ColdStartPoolRequest struct {
	UserID string
	N      int
}

type ColdStartPoolResponse struct {
	ItemIDs  []string `json:"item_ids"`
	Strategy string   `json:"strategy"`
	Count    int      `json:"count"`
}

type FirstScreenRequest struct {
	UserID string
	N      int
}

type FirstScreenResponse struct {
	ItemIDs []string `json:"item_ids"`
	Count   int      `json:"count"`
}

type CandidateItem struct {
	ItemID              string
	Timestamp           time.Time
	Labels              any
	ProfileCompleteness float64
	QualityScore        float64
	IsVerified          bool
	LikeRate            float64
	BlockRate           float64
}
