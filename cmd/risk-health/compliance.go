package main

import (
	"context"
)

func (s *Store) CheckCompliance(ctx context.Context, userID, itemID string) (*ComplianceResponse, error) {
	resp := &ComplianceResponse{Allowed: true, Reasons: []string{}, Warnings: []string{}}

	// Check user age
	age, err := s.GetUserAge(ctx, userID)
	if err == nil && age > 0 && age < 18 {
		resp.Allowed = false
		resp.Reasons = append(resp.Reasons, "user age < 18")
	}

	// Check item risk score
	riskResp, err := s.GetRiskScore(ctx, itemID)
	if err == nil && riskResp.RiskScore > 0.3 {
		resp.Allowed = false
		resp.Reasons = append(resp.Reasons, "item risk_score > 0.3")
	}

	// Check user quality flag
	ok, err := s.GetUserQualityFlag(ctx, userID)
	if err == nil && !ok {
		resp.Warnings = append(resp.Warnings, "user is_high_quality=false")
	}

	// Check item quality flag
	ok, err = s.GetItemQualityFlag(ctx, itemID)
	if err == nil && !ok {
		resp.Warnings = append(resp.Warnings, "item is_high_quality=false")
	}

	return resp, nil
}
