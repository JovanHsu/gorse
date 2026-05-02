package master

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorse-io/gorse/config"
)

func TestSidecarClient_GetFatigueRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health/fatigue_rate" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(FatigueRateResponse{
			FatigueRate:   0.15,
			FatiguedUsers: 30,
			TotalUsers:    200,
		})
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{RiskHealth: srv.URL}, nil)
	resp, err := client.GetFatigueRate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FatigueRate != 0.15 {
		t.Errorf("expected fatigue rate 0.15, got %v", resp.FatigueRate)
	}
	if resp.FatiguedUsers != 30 {
		t.Errorf("expected 30 fatigued users, got %v", resp.FatiguedUsers)
	}
}

func TestSidecarClient_GetFatigueRate_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{RiskHealth: srv.URL}, nil)
	_, err := client.GetFatigueRate(context.Background())
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestSidecarClient_GetFatigueRate_Unavailable(t *testing.T) {
	client := NewSidecarClient(&config.SidecarConfig{RiskHealth: ""}, nil)
	_, err := client.GetFatigueRate(context.Background())
	if err == nil {
		t.Error("expected error for unconfigured sidecar, got nil")
	}
}

func TestSidecarClient_GetPoolCoverage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health/pool_coverage" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(PoolCoverageResponse{
			Pools: map[string]any{"pool_a": "data"},
		})
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{RiskHealth: srv.URL}, nil)
	resp, err := client.GetPoolCoverage(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Pools == nil {
		t.Error("expected non-nil pools")
	}
}

func TestSidecarClient_GetSDBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health/sd_balance" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(SDBalanceResponse{
			FemaleExposureConcentration: 0.45,
			RecommendedThreshold:        0.5,
			Status:                      "ok",
		})
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{RiskHealth: srv.URL}, nil)
	resp, err := client.GetSDBalance(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
}

func TestSidecarClient_GetABReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ab/report" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		exp := r.URL.Query().Get("experiment")
		if exp == "" {
			http.Error(w, "missing experiment", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(ABReport{
			Experiment: exp,
			Groups: map[string]ABGroupReport{
				"control": {MatchRate: 0.10, SampleSize: 1000, Lift: ""},
				"A":       {MatchRate: 0.15, SampleSize: 1000, Lift: "+50.0%"},
			},
		})
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{ABExperiment: srv.URL}, nil)
	resp, err := client.GetABReport(context.Background(), "test_exp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Experiment != "test_exp" {
		t.Errorf("expected experiment 'test_exp', got %q", resp.Experiment)
	}
	if len(resp.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(resp.Groups))
	}
}

func TestSidecarClient_GetABReport_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{ABExperiment: srv.URL}, nil)
	_, err := client.GetABReport(context.Background(), "test")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestSidecarClient_RecordABMetric(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ab/metrics" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{ABExperiment: srv.URL}, nil)
	err := client.RecordABMetric(context.Background(), "exp1", "user1", "match", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSidecarClient_GetColdStartPool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cold_start_pool/user123" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(ColdStartPoolResponse{
			ItemIDs:  []string{"item1", "item2"},
			Strategy: "cold_start",
			Count:    2,
		})
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{ColdStart: srv.URL}, nil)
	resp, err := client.GetColdStartPool(context.Background(), "user123", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ItemIDs) != 2 {
		t.Errorf("expected 2 items, got %d", len(resp.ItemIDs))
	}
	if resp.Strategy != "cold_start" {
		t.Errorf("expected strategy 'cold_start', got %q", resp.Strategy)
	}
}

func TestSidecarClient_GetFirstScreen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/first_screen/user123" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(FirstScreenResponse{
			ItemIDs: []string{"item1", "item2", "item3"},
			Count:   3,
		})
	}))
	defer srv.Close()

	client := NewSidecarClient(&config.SidecarConfig{ColdStart: srv.URL}, nil)
	resp, err := client.GetFirstScreen(context.Background(), "user123", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Count != 3 {
		t.Errorf("expected 3 items, got %d", resp.Count)
	}
}

func TestSidecarClient_ColdStart_Unavailable(t *testing.T) {
	client := NewSidecarClient(&config.SidecarConfig{ColdStart: ""}, nil)
	_, err := client.GetColdStartPool(context.Background(), "user1", 20)
	if err == nil {
		t.Error("expected error for unconfigured cold-start sidecar, got nil")
	}
	_, err = client.GetFirstScreen(context.Background(), "user1", 20)
	if err == nil {
		t.Error("expected error for unconfigured cold-start sidecar, got nil")
	}
}

func TestSidecarClient_AB_Unavailable(t *testing.T) {
	client := NewSidecarClient(&config.SidecarConfig{ABExperiment: ""}, nil)
	_, err := client.GetABReport(context.Background(), "exp1")
	if err == nil {
		t.Error("expected error for unconfigured ab-experiment sidecar, got nil")
	}
	err = client.RecordABMetric(context.Background(), "exp1", "user1", "metric", 1.0)
	if err == nil {
		t.Error("expected error for unconfigured ab-experiment sidecar, got nil")
	}
}

func TestSidecarClient_RiskHealth_Unavailable(t *testing.T) {
	client := NewSidecarClient(&config.SidecarConfig{RiskHealth: ""}, nil)
	_, err := client.GetFatigueRate(context.Background())
	if err == nil {
		t.Error("expected error for unconfigured risk-health sidecar, got nil")
	}
	_, err = client.GetPoolCoverage(context.Background())
	if err == nil {
		t.Error("expected error for unconfigured risk-health sidecar, got nil")
	}
	_, err = client.GetSDBalance(context.Background())
	if err == nil {
		t.Error("expected error for unconfigured risk-health sidecar, got nil")
	}
}
