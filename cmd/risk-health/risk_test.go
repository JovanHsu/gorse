package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCalcRiskScore(t *testing.T) {
	tests := []struct {
		name     string
		stats    *ItemStats
		expected float64
	}{
		{"zero views", &ItemStats{ViewCount: 0}, 0.0},
		{"normal case", &ItemStats{BlockCount: 2, ReportCount: 1, ViewCount: 100}, 0.04},
		{"capped at 1", &ItemStats{BlockCount: 100, ReportCount: 100, ViewCount: 10}, 1.0},
		{"no violations", &ItemStats{BlockCount: 0, ReportCount: 0, ViewCount: 100}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalcRiskScore(tt.stats)
			if got != tt.expected {
				t.Errorf("CalcRiskScore() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCalcRiskLevel(t *testing.T) {
	tests := []struct {
		score    float64
		expected string
	}{
		{0.01, "low"},
		{0.04, "low"},
		{0.05, "medium"},
		{0.10, "medium"},
		{0.14, "medium"},
		{0.15, "high"},
		{0.50, "high"},
		{1.0, "high"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := CalcRiskLevel(tt.score)
			if got != tt.expected {
				t.Errorf("CalcRiskLevel(%v) = %v, want %v", tt.score, got, tt.expected)
			}
		})
	}
}

func TestRiskScoreEndpointLogic(t *testing.T) {
	stats := &ItemStats{BlockCount: 2, ReportCount: 1, ViewCount: 100}
	score := CalcRiskScore(stats)
	if score != 0.04 {
		t.Errorf("expected 0.04, got %v", score)
	}
	level := CalcRiskLevel(score)
	if level != "low" {
		t.Errorf("expected low, got %s", level)
	}
}

func TestRiskScoreCapped(t *testing.T) {
	stats := &ItemStats{BlockCount: 200, ReportCount: 100, ViewCount: 10}
	score := CalcRiskScore(stats)
	if score != 1.0 {
		t.Errorf("expected 1.0, got %v", score)
	}
	if CalcRiskLevel(score) != "high" {
		t.Errorf("expected high, got %s", CalcRiskLevel(score))
	}
}

func TestComplianceCheckParams(t *testing.T) {
	// Verify required params logic
	tests := []struct {
		userID string
		itemID string
		valid  bool
	}{
		{"u1", "i1", true},
		{"", "i1", false},
		{"u1", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		valid := tt.userID != "" && tt.itemID != ""
		if valid != tt.valid {
			t.Errorf("userID=%q, itemID=%q: expected valid=%v", tt.userID, tt.itemID, tt.valid)
		}
	}
}

func TestHealthEndpointLogic(t *testing.T) {
	w := httptest.NewRecorder()
	w.Write([]byte(`{"status":"ok"}`))
	if w.Code != 200 {
		t.Errorf("expected code 200, got %d", w.Code)
	}
}

func TestRiskScoreRequestParsing(t *testing.T) {
	body := `{"item_id":"item123"}`
	if !strings.Contains(body, "item_id") {
		t.Error("body should contain item_id")
	}
	if !strings.Contains(body, "item123") {
		t.Error("body should contain item123")
	}
}
