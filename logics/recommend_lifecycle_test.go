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
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logics

import (
	"testing"
	"time"

	"github.com/gorse-io/gorse/common/expression"
	"github.com/gorse-io/gorse/config"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// computeFatigueBoost is a test helper that mirrors the boost logic in recommendWithPools.
func computeFatigueBoost(swipeCount, triggerSwipes int) float64 {
	triggerThreshold := float64(triggerSwipes)
	if triggerThreshold <= 0 {
		triggerThreshold = 50
	}
	boost := 1.0 + float64(swipeCount)/triggerThreshold
	if boost > 3.0 {
		boost = 3.0
	}
	return boost
}

func TestFatigueBoost(t *testing.T) {
	tests := []struct {
		name           string
		swipeCount     int
		triggerSwipes  int
		expectedBoost  float64
	}{
		{"no swipes", 0, 50, 1.0},
		{"25 swipes (half threshold)", 25, 50, 1.5},
		{"50 swipes (at threshold)", 50, 50, 2.0},
		{"75 swipes (over threshold)", 75, 50, 2.5},
		{"100 swipes (capped at 3x)", 100, 50, 3.0},
		{"zero trigger threshold defaults to 50", 50, 0, 2.0},
		{"150 swipes (capped)", 150, 50, 3.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			boost := computeFatigueBoost(tc.swipeCount, tc.triggerSwipes)
			assert.InDelta(t, tc.expectedBoost, boost, 0.01)
		})
	}
}

func TestLifecycleClassifier_DetectType(t *testing.T) {
	cfg := config.LifecycleConfig{Enabled: true, CacheTTL: 5 * time.Minute}
	dataSource := config.DataSourceConfig{
		PositiveFeedbackTypes: []expression.FeedbackTypeExpression{
			expression.MustParseFeedbackTypeExpression("like"),
			expression.MustParseFeedbackTypeExpression("match"),
		},
	}
	fatigueCfg := config.FatigueConfig{
		Enabled:       true,
		TriggerSwipes: 50,
		TriggerDays:   7,
	}
	vipCfg := config.VIPConfig{
		Enabled:       true,
		VIPLabelKey:   "tier",
		VIPLabelValue: "vip",
	}
	pools := []config.RecallPoolConfig{
		{Name: "cold_start", LifecycleTypes: []string{"cold"}, BaseWeight: 0.8, Recommenders: []string{"latest"}, ExploreRatio: 0.3},
		{Name: "stable", LifecycleTypes: []string{"stable", "light"}, BaseWeight: 0.6, Recommenders: []string{"collaborative"}, ExploreRatio: 0.2},
		{Name: "fatigue", LifecycleTypes: []string{"fatigue"}, BaseWeight: 0.6, Recommenders: []string{"fatigue_breaker"}, ExploreRatio: 0.6},
		{Name: "vip", LifecycleTypes: []string{"vip"}, BaseWeight: 0.7, Recommenders: []string{"vip_quality_pool"}, ExploreRatio: 0.1},
	}

	classifier := NewLifecycleClassifier(cfg, dataSource, pools, fatigueCfg, vipCfg, nil, nil)

	tests := []struct {
		name           string
		labels         any
		feedbacks      []data.Feedback
		expectedType   LifecycleType
	}{
		{
			name:          "cold start - no feedback",
			labels:        nil,
			feedbacks:     []data.Feedback{},
			expectedType:  LifecycleCold,
		},
		{
			name:   "cold start - few likes",
			labels: nil,
			feedbacks: []data.Feedback{
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
			},
			expectedType: LifecycleCold,
		},
		{
			name:   "light user",
			labels: nil,
			feedbacks: []data.Feedback{
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
			},
			expectedType: LifecycleLight,
		},
		{
			name:   "stable user - many likes",
			labels: nil,
			feedbacks: func() []data.Feedback {
				fs := make([]data.Feedback, 25)
				for i := range fs {
					fs[i] = data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()}
				}
				return fs
			}(),
			expectedType: LifecycleStable,
		},
		{
			name:   "fatigue user - stable but no match in 8 days",
			labels: nil,
			feedbacks: func() []data.Feedback {
				fs := make([]data.Feedback, 25)
				for i := range fs {
					fs[i] = data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now().Add(-8 * 24 * time.Hour)}
				}
				// Add a match 8 days ago
				fs = append(fs, data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: "match"}, Timestamp: time.Now().Add(-8 * 24 * time.Hour)})
				return fs
			}(),
			expectedType: LifecycleFatigue,
		},
		{
			name:   "VIP user - has tier label",
			labels: map[string]any{"tier": "vip", "name": "Bob"},
			feedbacks: []data.Feedback{
				{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()},
			},
			expectedType: LifecycleVIP,
		},
		{
			name:   "non-VIP - has tier label but wrong value",
			labels: map[string]any{"tier": "gold"},
			feedbacks: func() []data.Feedback {
				fs := make([]data.Feedback, 25)
				for i := range fs {
					fs[i] = data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now()}
				}
				return fs
			}(),
			expectedType: LifecycleStable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			user := data.User{UserId: "user1", Labels: tc.labels}
			lt := classifier.detectType(nil, user, tc.feedbacks, len(tc.feedbacks))
			assert.Equal(t, tc.expectedType, lt)
		})
	}
}

func TestLifecycleClassifier_ComputePoolBlend(t *testing.T) {
	cfg := config.LifecycleConfig{Enabled: true}
	dataSource := config.DataSourceConfig{}
	fatigueCfg := config.FatigueConfig{}
	vipCfg := config.VIPConfig{}
	pools := []config.RecallPoolConfig{
		{Name: "cold_start", LifecycleTypes: []string{"cold"}, BaseWeight: 0.8, ExploreRatio: 0.3},
		{Name: "stable", LifecycleTypes: []string{"stable", "light"}, BaseWeight: 0.6, ExploreRatio: 0.2},
		{Name: "fatigue", LifecycleTypes: []string{"fatigue"}, BaseWeight: 0.6, ExploreRatio: 0.6},
		{Name: "vip", LifecycleTypes: []string{"vip"}, BaseWeight: 0.7, ExploreRatio: 0.1},
	}

	classifier := NewLifecycleClassifier(cfg, dataSource, pools, fatigueCfg, vipCfg, nil, nil)

	tests := []struct {
		name             string
		lifecycleType   LifecycleType
		expectedPools    []string
		totalWeight      float64
	}{
		{
			name:           "cold user gets cold_start pool",
			lifecycleType:  LifecycleCold,
			expectedPools:  []string{"cold_start"},
			totalWeight:    0.8,
		},
		{
			name:           "stable user gets stable pool",
			lifecycleType:  LifecycleStable,
			expectedPools:  []string{"stable"},
			totalWeight:    0.6,
		},
		{
			name:           "fatigue user gets fatigue pool",
			lifecycleType:  LifecycleFatigue,
			expectedPools:  []string{"fatigue"},
			totalWeight:    0.6,
		},
		{
			name:           "VIP user gets vip pool",
			lifecycleType:  LifecycleVIP,
			expectedPools:  []string{"vip"},
			totalWeight:    0.7,
		},
		{
			name:           "light user gets stable pool (fallback)",
			lifecycleType:  LifecycleLight,
			expectedPools:  []string{"stable"},
			totalWeight:    0.6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			weights, _ := classifier.computePoolBlend(tc.lifecycleType)
			require.Len(t, weights, len(tc.expectedPools))
			for _, poolName := range tc.expectedPools {
				_, ok := weights[poolName]
				assert.True(t, ok, "expected pool %s in weights", poolName)
			}
			// Check total weight is normalized
			total := 0.0
			for _, w := range weights {
				total += w
			}
			assert.InDelta(t, 1.0, total, 0.001)
		})
	}
}

func TestLifecycleClassifier_GetPoolsForProfile(t *testing.T) {
	cfg := config.LifecycleConfig{Enabled: true}
	dataSource := config.DataSourceConfig{}
	fatigueCfg := config.FatigueConfig{}
	vipCfg := config.VIPConfig{}
	pools := []config.RecallPoolConfig{
		{Name: "cold_start", LifecycleTypes: []string{"cold"}, BaseWeight: 0.8, Recommenders: []string{"latest"}, ExploreRatio: 0.3},
		{Name: "stable", LifecycleTypes: []string{"stable"}, BaseWeight: 0.6, Recommenders: []string{"collaborative", "user-to-user"}, ExploreRatio: 0.2},
	}

	classifier := NewLifecycleClassifier(cfg, dataSource, pools, fatigueCfg, vipCfg, nil, nil)

	profile := &LifecycleProfile{
		UserId: "user1",
		Primary: LifecycleCold,
		PoolWeights: map[string]float64{
			"cold_start": 1.0,
		},
		ExploreRatio: 0.3,
	}

	blends := classifier.GetPoolsForProfile(profile)
	require.Len(t, blends, 1)
	assert.Equal(t, "cold_start", blends[0].PoolName)
	assert.Equal(t, 1.0, blends[0].Weight)
	assert.Equal(t, []string{"latest"}, blends[0].Recommenders)
	assert.Equal(t, 0.3, blends[0].ExploreRatio)
}

func TestFatigueDetection(t *testing.T) {
	cfg := config.LifecycleConfig{Enabled: true}
	dataSource := config.DataSourceConfig{
		PositiveFeedbackTypes: []expression.FeedbackTypeExpression{
			expression.MustParseFeedbackTypeExpression("like"),
			expression.MustParseFeedbackTypeExpression("match"),
		},
	}
	fatigueCfg := config.FatigueConfig{
		Enabled:       true,
		TriggerSwipes: 5, // small for testing
		TriggerDays:   7,
	}
	vipCfg := config.VIPConfig{}
	pools := []config.RecallPoolConfig{
		{Name: "stable", LifecycleTypes: []string{"stable"}, BaseWeight: 0.6, ExploreRatio: 0.2},
		{Name: "fatigue", LifecycleTypes: []string{"fatigue"}, BaseWeight: 0.6, ExploreRatio: 0.6},
	}

	classifier := NewLifecycleClassifier(cfg, dataSource, pools, fatigueCfg, vipCfg, nil, nil)

	t.Run("triggers by consecutive swipes without match", func(t *testing.T) {
		// 6 consecutive likes with no match -> fatigued
		feedbacks := make([]data.Feedback, 6)
		for i := range feedbacks {
			feedbacks[i] = data.Feedback{
				FeedbackKey: data.FeedbackKey{FeedbackType: "like"},
				Timestamp:   time.Now().Add(-time.Duration(i) * time.Hour),
			}
		}
		assert.True(t, classifier.isFatigued(feedbacks))
	})

	t.Run("does not trigger with recent match", func(t *testing.T) {
		feedbacks := make([]data.Feedback, 6)
		for i := range feedbacks {
			fType := "like"
			if i == 0 {
				fType = "match" // most recent is a match
			}
			feedbacks[i] = data.Feedback{
				FeedbackKey: data.FeedbackKey{FeedbackType: fType},
				Timestamp:   time.Now().Add(-time.Duration(i) * time.Hour),
			}
		}
		assert.False(t, classifier.isFatigued(feedbacks))
	})

	t.Run("triggers by days without match", func(t *testing.T) {
		feedbacks := []data.Feedback{
			{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now().Add(-8 * 24 * time.Hour)},
			{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}, Timestamp: time.Now().Add(-10 * 24 * time.Hour)},
			{FeedbackKey: data.FeedbackKey{FeedbackType: "match"}, Timestamp: time.Now().Add(-10 * 24 * time.Hour)},
		}
		assert.True(t, classifier.isFatigued(feedbacks))
	})
}

func TestVIPDetection(t *testing.T) {
	cfg := config.LifecycleConfig{Enabled: true}
	dataSource := config.DataSourceConfig{}
	fatigueCfg := config.FatigueConfig{}
	vipCfg := config.VIPConfig{
		Enabled:       true,
		VIPLabelKey:   "tier",
		VIPLabelValue: "vip",
	}
	pools := []config.RecallPoolConfig{}

	classifier := NewLifecycleClassifier(cfg, dataSource, pools, fatigueCfg, vipCfg, nil, nil)

	tests := []struct {
		name     string
		labels   any
		expected bool
	}{
		{"VIP label set", map[string]any{"tier": "vip"}, true},
		{"empty labels", map[string]any{}, false},
		{"nil labels", nil, false},
		{"wrong tier value", map[string]any{"tier": "premium"}, false},
		{"nested tier", map[string]any{"profile": map[string]any{"tier": "vip"}}, false},
		{"non-string tier", map[string]any{"tier": 1}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			user := data.User{UserId: "user1", Labels: tc.labels}
			assert.Equal(t, tc.expected, classifier.isVIP(user))
		})
	}
}
