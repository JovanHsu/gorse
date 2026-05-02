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
	"context"
	"time"

	"github.com/gorse-io/gorse/config"
	"github.com/gorse-io/gorse/logics"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// BackgroundServiceManager manages lifecycle-aware background computation services.
// It owns the goroutines for stats computation (behavior, item quality, success rate, etc.)
// and ensures they are cleanly started and stopped with the master node.
type BackgroundServiceManager struct {
	behaviorStats  *logics.BehaviorStatsComputer
	successRate    *logics.SuccessRateComputer
	cvrReporter    *CVRReporter

	// For future extensibility: add more services here.
	// e.g., lifecycleClassifier *logics.LifecycleClassifier
}

// NewBackgroundServiceManager creates a manager with all enabled background services.
func NewBackgroundServiceManager(
	cfg config.RecommendConfig,
	redisClient *redis.Client,
	dataClient data.Database,
	cvrReporter *CVRReporter,
) *BackgroundServiceManager {
	m := &BackgroundServiceManager{cvrReporter: cvrReporter}

	// BehaviorStatsComputer: computes user behavior statistics (right_swipe_rate,
	// behavior_stability, explore_ratio) and writes to Redis + User.Labels.
	if cfg.Behavior.Enabled {
		m.behaviorStats = logics.NewBehaviorStatsComputer(
			cfg.Behavior,
			cfg.ItemStats,
			redisClient,
			dataClient,
		)
		zap.L().Info("behavior stats computer enabled",
			zap.Int("min_swipes", cfg.Behavior.MinSwipeCount))
	}

	// SuccessRateComputer: computes per-item like/match rates and writes to Redis.
	if cfg.SuccessRate.Enabled || cfg.ItemStats.Enabled {
		m.successRate = logics.NewSuccessRateComputer(
			cfg.SuccessRate,
			cfg.ItemStats,
			redisClient,
			dataClient,
		)
		zap.L().Info("success rate computer enabled",
			zap.Bool("success_rate", cfg.SuccessRate.Enabled),
			zap.Bool("item_stats", cfg.ItemStats.Enabled))
	}

	return m
}

// Start launches all background computation goroutines.
// Call this after all database connections are established.
func (m *BackgroundServiceManager) Start(ctx context.Context) {
	if m.behaviorStats != nil {
		// Default: recompute behavior stats every 5 minutes.
		m.behaviorStats.Start(5 * time.Minute)
		zap.L().Info("behavior stats computer started",
			zap.Duration("interval", 5*time.Minute))
	}

	if m.successRate != nil {
		// Compute success rates hourly (less frequent than behavior stats).
		m.successRate.Start(1 * time.Hour)
		zap.L().Info("success rate computer started",
			zap.Duration("interval", 1*time.Hour))
	}

	if m.cvrReporter != nil {
		// Report CVR metrics every 15 minutes.
		m.cvrReporter.Start(ctx)
		zap.L().Info("cvr reporter started",
			zap.Duration("interval", 15*time.Minute))
	}
}

// Stop gracefully stops all background services.
// Call this during master shutdown.
func (m *BackgroundServiceManager) Stop() {
	if m.behaviorStats != nil {
		m.behaviorStats.Stop()
		zap.L().Info("behavior stats computer stopped")
	}
	if m.successRate != nil {
		m.successRate.Stop()
		zap.L().Info("success rate computer stopped")
	}
	if m.cvrReporter != nil {
		m.cvrReporter.Stop()
		zap.L().Info("cvr reporter stopped")
	}
}
