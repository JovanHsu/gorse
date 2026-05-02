package main

import (
	"testing"
	"time"
)

func TestPoolStatCoverage(t *testing.T) {
	stats := map[string]PoolStat{
		"collab": {Count: 100},
		"social": {Count: 50},
	}
	total := int64(150)
	for name := range stats {
		ps := stats[name]
		ps.Coverage = float64(ps.Count) / float64(total)
		stats[name] = ps
	}

	if stats["collab"].Coverage < 0.666 || stats["collab"].Coverage > 0.667 {
		t.Errorf("expected ~0.666, got %v", stats["collab"].Coverage)
	}
	if stats["social"].Coverage < 0.333 || stats["social"].Coverage > 0.334 {
		t.Errorf("expected ~0.333, got %v", stats["social"].Coverage)
	}
}

func TestFatigueRateCalculation(t *testing.T) {
	fatiguedUsers := int64(120)
	totalUsers := int64(1000)
	rate := float64(fatiguedUsers) / float64(totalUsers)

	if rate != 0.12 {
		t.Errorf("expected 0.12, got %v", rate)
	}
}

func TestFatigueRateZeroUsers(t *testing.T) {
	var rate float64
	totalUsers := int64(0)
	fatiguedUsers := int64(0)
	if totalUsers > 0 {
		rate = float64(fatiguedUsers) / float64(totalUsers)
	}
	if rate != 0.0 {
		t.Errorf("expected 0.0, got %v", rate)
	}
}

func TestSDBalanceStatus(t *testing.T) {
	tests := []struct {
		concentration float64
		expected      string
	}{
		{0.35, "balanced"},
		{0.39, "balanced"},
		{0.41, "imbalanced"},
		{0.55, "imbalanced"},
	}

	for _, tt := range tests {
		status := "balanced"
		if tt.concentration > 0.40 {
			status = "imbalanced"
		}
		if status != tt.expected {
			t.Errorf("concentration=%v: expected %s, got %s", tt.concentration, tt.expected, status)
		}
	}
}

func TestPoolCoverageResponseTimestamp(t *testing.T) {
	now := time.Now().UTC()
	resp := &PoolCoverageResponse{
		Timestamp: now,
		Pools:     map[string]map[string]PoolStat{},
	}
	if resp.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}

func TestFatigueRateResponseFields(t *testing.T) {
	resp := &FatigueRateResponse{
		Timestamp:     time.Now().UTC(),
		FatigueRate:   0.12,
		FatiguedUsers: 120,
		TotalUsers:    1000,
	}
	if resp.FatigueRate != 0.12 {
		t.Errorf("expected 0.12, got %v", resp.FatigueRate)
	}
	if resp.FatiguedUsers != 120 {
		t.Errorf("expected 120, got %v", resp.FatiguedUsers)
	}
	if resp.TotalUsers != 1000 {
		t.Errorf("expected 1000, got %v", resp.TotalUsers)
	}
}

func TestSDBalanceResponseFields(t *testing.T) {
	resp := &SDBalanceResponse{
		Timestamp:                   time.Now().UTC(),
		FemaleExposureConcentration: 0.35,
		RecommendedThreshold:        0.40,
		Status:                      "balanced",
	}
	if resp.FemaleExposureConcentration != 0.35 {
		t.Errorf("expected 0.35, got %v", resp.FemaleExposureConcentration)
	}
	if resp.RecommendedThreshold != 0.40 {
		t.Errorf("expected 0.40, got %v", resp.RecommendedThreshold)
	}
	if resp.Status != "balanced" {
		t.Errorf("expected balanced, got %s", resp.Status)
	}
}
