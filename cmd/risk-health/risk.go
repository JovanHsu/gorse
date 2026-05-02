package main

import (
	"context"
)

func CalcRiskScore(stats *ItemStats) float64 {
	if stats.ViewCount == 0 {
		return 0.0
	}
	score := float64(stats.BlockCount+stats.ReportCount*2) / float64(stats.ViewCount)
	if score > 1.0 {
		score = 1.0
	}
	return score
}

func CalcRiskLevel(score float64) string {
	if score < 0.05 {
		return "low"
	}
	if score < 0.15 {
		return "medium"
	}
	return "high"
}

func (s *Store) GetRiskScore(ctx context.Context, itemID string) (*RiskScoreResponse, error) {
	stats, err := s.GetItemStats(ctx, itemID)
	if err != nil {
		return nil, err
	}
	score := CalcRiskScore(stats)
	return &RiskScoreResponse{
		ItemID:    itemID,
		RiskScore: score,
		RiskLevel: CalcRiskLevel(score),
	}, nil
}
