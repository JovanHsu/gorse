// Copyright 2025 gorse Project Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package master

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorse-io/gorse/config"
	"github.com/redis/go-redis/v9"
)

// SidecarClient calls sidecar HTTP services.
type SidecarClient struct {
	httpClient   *http.Client
	redisClient  RedisScanner
	ABExperiment string
	RiskHealth   string
	ColdStart    string
}

// RedisScanner abstracts Redis scan operations for testing.
type RedisScanner interface {
	Scan(ctx context.Context, cursor uint64, match string, count int64) ScanIterator
}

// ScanIterator is compatible with the result of Scan(...).Iterator().
type ScanIterator interface {
	Next(ctx context.Context) bool
	Val() string
	Err() error
}

// redisClientAdapter wraps *redis.Client to implement RedisScanner.
type redisClientAdapter struct {
	client *redis.Client
}

func (a *redisClientAdapter) Scan(ctx context.Context, cursor uint64, match string, count int64) ScanIterator {
	return a.client.Scan(ctx, cursor, match, count).Iterator()
}

// NewSidecarClient creates a new SidecarClient.
func NewSidecarClient(cfg *config.SidecarConfig, redisClient *redis.Client) *SidecarClient {
	return &SidecarClient{
		httpClient:   &http.Client{Timeout: 5 * time.Second},
		redisClient:  &redisClientAdapter{client: redisClient},
		ABExperiment: cfg.ABExperiment,
		RiskHealth:   cfg.RiskHealth,
		ColdStart:    cfg.ColdStart,
	}
}

// ---- A/B Experiment ----

// ABReport is the response from the ab-experiment report endpoint.
type ABReport struct {
	Experiment string                   `json:"experiment"`
	Groups     map[string]ABGroupReport `json:"groups"`
}

// ABGroupReport contains metrics for a single experiment group.
type ABGroupReport struct {
	MatchRate  float64 `json:"match_rate,omitempty"`
	SampleSize int64   `json:"sample_size"`
	Lift       string  `json:"lift,omitempty"`
}

// GetExperiments scans Redis for all tracked experiment names.
func (s *SidecarClient) GetExperiments(ctx context.Context) ([]string, error) {
	if s.redisClient == nil {
		return nil, nil
	}
	// Scan for ab:metrics:* keys and extract unique experiment names.
	seen := make(map[string]struct{})
	var experiments []string
	iter := s.redisClient.Scan(ctx, 0, "ab:metrics:*", 100)
	for iter.Next(ctx) {
		key := iter.Val()
		// Key format: ab:metrics:<experiment>:<group>:<type>:<metric>
		parts := strings.SplitN(key, ":", 4)
		if len(parts) >= 3 && parts[0] == "ab" && parts[1] == "metrics" {
			exp := parts[2]
			if _, ok := seen[exp]; !ok {
				seen[exp] = struct{}{}
				experiments = append(experiments, exp)
			}
		}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("redis scan error: %w", err)
	}
	return experiments, nil
}

// GetABReport returns the report for a specific experiment.
func (s *SidecarClient) GetABReport(ctx context.Context, experiment string) (*ABReport, error) {
	url := fmt.Sprintf("%s/ab/report?experiment=%s", s.ABExperiment, experiment)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("ab-experiment unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ab-experiment returned %d: %s", resp.StatusCode, string(body))
	}
	var r ABReport
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid ab report response: %w", err)
	}
	return &r, nil
}

// RecordABMetric records a metric for an A/B experiment.
func (s *SidecarClient) RecordABMetric(ctx context.Context, experiment, userID, metric string, value float64) error {
	url := fmt.Sprintf("%s/ab/metrics", s.ABExperiment)
	body, _ := json.Marshal(map[string]any{
		"experiment": experiment, "user_id": userID, "metric": metric, "value": value,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ab-experiment unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ab-experiment returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// ---- Risk Health ----

// FatigueRateResponse is the response from the fatigue_rate endpoint.
type FatigueRateResponse struct {
	Timestamp     time.Time `json:"timestamp"`
	FatigueRate   float64   `json:"fatigue_rate"`
	FatiguedUsers int64     `json:"fatigued_users"`
	TotalUsers    int64     `json:"total_users"`
}

// GetFatigueRate returns the current fatigue rate.
func (s *SidecarClient) GetFatigueRate(ctx context.Context) (*FatigueRateResponse, error) {
	url := fmt.Sprintf("%s/api/health/fatigue_rate", s.RiskHealth)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("risk-health unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("risk-health returned %d: %s", resp.StatusCode, string(body))
	}
	var r FatigueRateResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// PoolCoverageResponse is the response from the pool_coverage endpoint.
type PoolCoverageResponse struct {
	Timestamp time.Time `json:"timestamp"`
	Pools     any       `json:"pools"`
}

// GetPoolCoverage returns the pool coverage metrics.
func (s *SidecarClient) GetPoolCoverage(ctx context.Context) (*PoolCoverageResponse, error) {
	url := fmt.Sprintf("%s/api/health/pool_coverage", s.RiskHealth)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("risk-health unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("risk-health returned %d: %s", resp.StatusCode, string(body))
	}
	var r PoolCoverageResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SDBalanceResponse is the response from the sd_balance endpoint.
type SDBalanceResponse struct {
	Timestamp                  time.Time `json:"timestamp"`
	FemaleExposureConcentration float64   `json:"female_exposure_concentration"`
	RecommendedThreshold       float64   `json:"recommended_threshold"`
	Status                     string    `json:"status"`
}

// GetSDBalance returns the supply/demand balance metrics.
func (s *SidecarClient) GetSDBalance(ctx context.Context) (*SDBalanceResponse, error) {
	url := fmt.Sprintf("%s/api/health/sd_balance", s.RiskHealth)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("risk-health unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("risk-health returned %d: %s", resp.StatusCode, string(body))
	}
	var r SDBalanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ---- Cold Start ----

// ColdStartPoolResponse is the response from the cold_start_pool endpoint.
type ColdStartPoolResponse struct {
	ItemIDs  []string `json:"item_ids"`
	Strategy string   `json:"strategy"`
	Count    int      `json:"count"`
}

// GetColdStartPool returns cold start candidates for a user.
func (s *SidecarClient) GetColdStartPool(ctx context.Context, userID string, n int) (*ColdStartPoolResponse, error) {
	url := fmt.Sprintf("%s/cold_start_pool/%s?n=%d", s.ColdStart, userID, n)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("cold-start unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("cold-start returned %d: %s", resp.StatusCode, string(body))
	}
	var r ColdStartPoolResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// FirstScreenResponse is the response from the first_screen endpoint.
type FirstScreenResponse struct {
	ItemIDs []string `json:"item_ids"`
	Count   int      `json:"count"`
}

// GetFirstScreen returns first screen candidates for a user.
func (s *SidecarClient) GetFirstScreen(ctx context.Context, userID string, n int) (*FirstScreenResponse, error) {
	url := fmt.Sprintf("%s/first_screen/%s?n=%d", s.ColdStart, userID, n)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("cold-start unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("cold-start returned %d: %s", resp.StatusCode, string(body))
	}
	var r FirstScreenResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}
