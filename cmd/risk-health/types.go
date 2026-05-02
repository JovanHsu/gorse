package main

import "time"

type RiskScoreRequest struct {
	ItemID string `json:"item_id"`
}

type RiskScoreResponse struct {
	ItemID     string  `json:"item_id"`
	RiskScore  float64 `json:"risk_score"`
	RiskLevel  string  `json:"risk_level"`
}

type ComplianceRequest struct {
	UserID string `json:"user_id"`
	ItemID string `json:"item_id"`
}

type ComplianceResponse struct {
	Allowed   bool     `json:"allowed"`
	Reasons   []string `json:"reasons"`
	Warnings  []string `json:"warnings"`
}

type PoolCoverageResponse struct {
	Timestamp time.Time              `json:"timestamp"`
	Pools     map[string]map[string]PoolStat `json:"pools"`
}

type PoolStat struct {
	Count    int64   `json:"count"`
	Coverage float64 `json:"coverage"`
}

type FatigueRateResponse struct {
	Timestamp     time.Time `json:"timestamp"`
	FatigueRate   float64   `json:"fatigue_rate"`
	FatiguedUsers int64     `json:"fatigued_users"`
	TotalUsers    int64     `json:"total_users"`
}

type SDBalanceResponse struct {
	Timestamp                     time.Time `json:"timestamp"`
	FemaleExposureConcentration   float64   `json:"female_exposure_concentration"`
	RecommendedThreshold          float64   `json:"recommended_threshold"`
	Status                        string    `json:"status"`
}

type ItemStats struct {
	LikeCount   int64 `json:"like_count"`
	BlockCount  int64 `json:"block_count"`
	ReportCount int64 `json:"report_count"`
	ViewCount   int64 `json:"view_count"`
}

type UserStats struct {
	SwipeCount   int64   `json:"swipe_count"`
	LastMatchAt  int64   `json:"last_match_at"`
}

type HealthResponse struct {
	Status string `json:"status"`
}
