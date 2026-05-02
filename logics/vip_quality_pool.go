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

package logics

import (
	"context"
	"math"
	"sort"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

// VIPQualityConfig controls the VIP quality pool recommender.
type VIPQualityConfig struct {
	// MinQualityScore filters out items below this quality threshold (0-1).
	MinQualityScore float64 `mapstructure:"min_quality_score"`
	// RequireVerified only returns verified users when true.
	RequireVerified bool `mapstructure:"require_verified"`
	// ActiveWithinDays filters out users inactive for more than this many days (0 = disabled).
	ActiveWithinDays int `mapstructure:"active_within_days"`
	// MaxResults caps how many candidates to fetch.
	MaxResults int `mapstructure:"max_results"`
	// ScoreBy controls scoring: "quality", "quality_recency", "engagement".
	ScoreBy string `mapstructure:"score_by"`
}

func defaultVIPQualityConfig() VIPQualityConfig {
	return VIPQualityConfig{
		MinQualityScore: 0,
		RequireVerified: false,
		ActiveWithinDays: 7,
		MaxResults: 200,
		ScoreBy: "quality_recency",
	}
}

// recommendVIPQualityPool returns high-quality users for VIP users.
// It prioritizes verified users with high quality_score, recent activity,
// and good engagement metrics (low block rate, high like rate from Redis).
func (r *Recommender) recommendVIPQualityPool(ctx context.Context) ([]cache.Score, string, error) {
	cfg := defaultVIPQualityConfig()
	if r.config.VIPQuality.MinQualityScore > 0 {
		cfg.MinQualityScore = r.config.VIPQuality.MinQualityScore
	}
	cfg.RequireVerified = r.config.VIPQuality.RequireVerified
	if r.config.VIPQuality.ActiveWithinDays > 0 {
		cfg.ActiveWithinDays = r.config.VIPQuality.ActiveWithinDays
	}
	if r.config.VIPQuality.MaxResults > 0 {
		cfg.MaxResults = r.config.VIPQuality.MaxResults
	}
	if r.config.VIPQuality.ScoreBy != "" {
		cfg.ScoreBy = r.config.VIPQuality.ScoreBy
	}

	excludeSet := r.ExcludeSet()

	// Determine target gender.
	targetGender := r.getTargetGender(ctx)

	// Fetch candidates.
	candidates, err := r.fetchVIPQualityCandidates(ctx, cfg, targetGender, excludeSet)
	if err != nil {
		return nil, "", err
	}

	if len(candidates) == 0 {
		return nil, VIPQualityPoolRecommender, nil
	}

	// Score.
	scored := r.scoreByVIPQuality(candidates, cfg)

	// Sort descending.
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	zap.L().Debug("vip_quality_pool",
		zap.String("user_id", r.userId),
		zap.Int("candidates", len(candidates)),
		zap.Int("results", len(scored)))

	return scored, VIPQualityPoolRecommender, nil
}

// vipCandidate holds a candidate item with parsed quality fields.
type vipCandidate struct {
	item          data.Item
	qualityScore  float64
	isVerified    bool
	likeRate      float64
	blockRate     float64
	photoCount    int
	profileFields int // how many profile fields are filled
}

func (r *Recommender) fetchVIPQualityCandidates(
	ctx context.Context,
	cfg VIPQualityConfig,
	targetGender string,
	excludeSet mapset.Set[string],
) ([]vipCandidate, error) {
	var candidates []vipCandidate

	activeCutoff := time.Time{}
	if cfg.ActiveWithinDays > 0 {
		activeCutoff = time.Now().AddDate(0, 0, -cfg.ActiveWithinDays)
	}

	fetchLimit := cfg.MaxResults * 3
	if fetchLimit <= 0 {
		fetchLimit = 600
	}

	var items []data.Item
	if targetGender != "" {
		// Fetch by gender category for efficiency.
		items, _ = r.dataClient.GetLatestItems(ctx, fetchLimit, []string{targetGender}, nil)
	} else {
		// No gender filter: fetch latest.
		items, _ = r.dataClient.GetLatestItems(ctx, fetchLimit, nil, nil)
	}

	for _, item := range items {
		if excludeSet.Contains(item.ItemId) {
			continue
		}
		c := r.parseVIPCandidate(item, cfg)
		if c == nil {
			continue
		}
		// Active recency filter.
		if !activeCutoff.IsZero() && !c.item.Timestamp.IsZero() && c.item.Timestamp.Before(activeCutoff) {
			continue
		}
		candidates = append(candidates, *c)
		if len(candidates) >= cfg.MaxResults {
			break
		}
	}

	return candidates, nil
}

func (r *Recommender) parseVIPCandidate(item data.Item, cfg VIPQualityConfig) *vipCandidate {
	c := &vipCandidate{item: item}

	if len(item.Categories) > 0 {
		cat := item.Categories[0]
		if cat != "M" && cat != "F" && cat != "O" {
			return nil // not a user item
		}
	}

	if labels, ok := item.Labels.(map[string]any); ok {
		// Quality score.
		if v, ok := labels["quality_score"].(float64); ok {
			c.qualityScore = v
		}

		// Verified flag.
		if v, ok := labels["is_verified"].(bool); ok {
			c.isVerified = v
		}
		if cfg.RequireVerified && !c.isVerified {
			return nil
		}

		// Photo count.
		if v, ok := labels["photo_count"].(float64); ok {
			c.photoCount = int(v)
		}

		// Like rate.
		if v, ok := labels["like_rate"].(float64); ok {
			c.likeRate = v
		}

		// Block rate.
		if v, ok := labels["block_rate"].(float64); ok {
			c.blockRate = v
		}

		// Profile completeness: count filled fields.
		fields := []string{
			"age", "city", "purpose", "photo_count",
			"quality_score", "is_verified",
		}
		for _, f := range fields {
			if _, ok := labels[f]; ok {
				c.profileFields++
			}
		}

		// If quality_score is 0, compute from profile fields.
		if c.qualityScore == 0 && c.profileFields > 0 {
			c.qualityScore = float64(c.profileFields) / float64(len(fields))
		}
	}

	// Hard quality filter.
	if c.qualityScore < cfg.MinQualityScore {
		return nil
	}

	return c
}

func (r *Recommender) scoreByVIPQuality(candidates []vipCandidate, cfg VIPQualityConfig) []cache.Score {
	// Find max timestamp for recency normalization.
	var maxTs time.Time
	for _, c := range candidates {
		if !c.item.Timestamp.IsZero() && c.item.Timestamp.After(maxTs) {
			maxTs = c.item.Timestamp
		}
	}
	if maxTs.IsZero() {
		maxTs = time.Now()
	}

	// Determine max profile fields for normalization.
	maxFields := 0
	for _, c := range candidates {
		if c.profileFields > maxFields {
			maxFields = c.profileFields
		}
	}
	if maxFields == 0 {
		maxFields = 1
	}

	results := make([]cache.Score, 0, len(candidates))
	for _, c := range candidates {
		var score float64

		switch cfg.ScoreBy {
		case "quality":
			score = c.qualityScore
		case "engagement":
			// Balance like rate with block rate penalty.
			score = c.likeRate - c.blockRate
			if score < 0 {
				score = 0
			}
		default: // "quality_recency"
			// Quality base.
			qualityBase := c.qualityScore
			// Bonus for verified (+0.1).
			if c.isVerified {
				qualityBase += 0.1
			}
			// Bonus for profile completeness.
			profileBonus := float64(c.profileFields) / float64(maxFields) * 0.1
			// Recency decay (30-day half-life).
			daysActive := maxTs.Sub(c.item.Timestamp).Hours() / 24
			recencyScore := math.Max(0, 1-daysActive/30)
			// Engagement bonus.
			engagementScore := c.likeRate - c.blockRate
			if engagementScore < 0 {
				engagementScore = 0
			}

			score = 0.5*math.Min(qualityBase+profileBonus, 1.0) +
				0.3*recencyScore +
				0.2*engagementScore
		}

		if score <= 0 {
			continue
		}

		results = append(results, cache.Score{Id: c.item.ItemId, Score: score})
	}

	// Deduplicate.
	seen := mapset.NewSet[string]()
	deduped := lo.Filter(results, func(s cache.Score, _ int) bool {
		if seen.Contains(s.Id) {
			return false
		}
		seen.Add(s.Id)
		return true
	})

	return deduped
}
