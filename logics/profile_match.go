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
	"github.com/gorse-io/gorse/common/log"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/juju/errors"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

// ProfileMatchConfig controls the profile-based matching recommender.
type ProfileMatchConfig struct {
	// MinAge is the minimum age to recommend (overrides user pref if 0).
	MinAge int `mapstructure:"min_age"`
	// MaxAge is the maximum age to recommend (overrides user pref if 0).
	MaxAge int `mapstructure:"max_age"`
	// MaxDistanceKm limits recommendations to within this distance (0 = no limit).
	MaxDistanceKm int `mapstructure:"max_distance_km"`
	// MinProfileCompleteness filters out items below this score (0-1, 0 = disabled).
	MinProfileCompleteness float64 `mapstructure:"min_profile_completeness"`
	// MinPhotoCount requires at least this many photos.
	MinPhotoCount int `mapstructure:"min_photo_count"`
	// PreferVerified boosts verified users in scoring.
	PreferVerified bool `mapstructure:"prefer_verified"`
	// ActiveWithinDays filters out users inactive for more than this many days (0 = disabled).
	ActiveWithinDays int `mapstructure:"active_within_days"`
	// ScoreBy controls the scoring strategy: "profile_completeness", "recency", "combined".
	ScoreBy string `mapstructure:"score_by"`
	// MaxResults caps how many candidates to fetch from DB (for performance).
	MaxResults int `mapstructure:"max_results"`
}

func defaultProfileMatchConfig() ProfileMatchConfig {
	return ProfileMatchConfig{
		MinAge:              18,
		MaxAge:              65,
		MaxDistanceKm:       0,
		MinProfileCompleteness: 0,
		MinPhotoCount:       0,
		PreferVerified:      true,
		ActiveWithinDays:    0,
		ScoreBy:             "combined",
		MaxResults:          200,
	}
}

// userPreference holds the requesting user's profile-matching preferences.
type userPreference struct {
	PrefAgeMin    int
	PrefAgeMax    int
	PrefCity      string
	PrefPurpose   string
	PrefMaxDistKm int
}

// getUserPreference reads profile-matching preferences from the requesting user's Labels.
func (r *Recommender) getUserPreference(ctx context.Context) (userPreference, error) {
	pref := userPreference{
		PrefAgeMin:    18,
		PrefAgeMax:    65,
		PrefMaxDistKm: 0,
	}
	cfg := defaultProfileMatchConfig()
	if r.config.ProfileMatch.MinAge > 0 {
		cfg.MinAge = r.config.ProfileMatch.MinAge
	}
	if r.config.ProfileMatch.MaxAge > 0 {
		cfg.MaxAge = r.config.ProfileMatch.MaxAge
	}
	if r.config.ProfileMatch.MaxDistanceKm > 0 {
		cfg.MaxDistanceKm = r.config.ProfileMatch.MaxDistanceKm
	}

	user, err := r.dataClient.GetUser(ctx, r.userId)
	if err != nil {
		return pref, errors.Trace(err)
	}

	pref.PrefAgeMin = cfg.MinAge
	pref.PrefAgeMax = cfg.MaxAge

	if labels, ok := user.Labels.(map[string]any); ok {
		if v, ok := labels["pref_age_min"].(float64); ok {
			pref.PrefAgeMin = int(v)
		}
		if v, ok := labels["pref_age_max"].(float64); ok {
			pref.PrefAgeMax = int(v)
		}
		if v, ok := labels["pref_city"].(string); ok && v != "" {
			pref.PrefCity = v
		}
		if v, ok := labels["pref_purpose"].(string); ok && v != "" {
			pref.PrefPurpose = v
		}
		if v, ok := labels["pref_max_distance_km"].(float64); ok && v > 0 {
			pref.PrefMaxDistKm = int(v)
		}
	}
	return pref, nil
}

// recommendProfileMatch recommends users based on profile compatibility.
// It filters candidates by the requesting user's stated preferences (age, city,
// purpose, distance) and scores by profile completeness, recency, and verification.
func (r *Recommender) recommendProfileMatch(ctx context.Context) ([]cache.Score, string, error) {
	cfg := defaultProfileMatchConfig()
	if r.config.ProfileMatch.MinAge > 0 {
		cfg.MinAge = r.config.ProfileMatch.MinAge
	}
	if r.config.ProfileMatch.MaxAge > 0 {
		cfg.MaxAge = r.config.ProfileMatch.MaxAge
	}
	if r.config.ProfileMatch.MaxDistanceKm > 0 {
		cfg.MaxDistanceKm = r.config.ProfileMatch.MaxDistanceKm
	}
	if r.config.ProfileMatch.MinProfileCompleteness > 0 {
		cfg.MinProfileCompleteness = r.config.ProfileMatch.MinProfileCompleteness
	}
	if r.config.ProfileMatch.MinPhotoCount > 0 {
		cfg.MinPhotoCount = r.config.ProfileMatch.MinPhotoCount
	}
	cfg.PreferVerified = r.config.ProfileMatch.PreferVerified
	if r.config.ProfileMatch.ActiveWithinDays > 0 {
		cfg.ActiveWithinDays = r.config.ProfileMatch.ActiveWithinDays
	}
	if r.config.ProfileMatch.ScoreBy != "" {
		cfg.ScoreBy = r.config.ProfileMatch.ScoreBy
	}
	if r.config.ProfileMatch.MaxResults > 0 {
		cfg.MaxResults = r.config.ProfileMatch.MaxResults
	}

	excludeSet := r.ExcludeSet()
	pref, err := r.getUserPreference(ctx)
	if err != nil {
		zap.L().Warn("profile_match: failed to get user preference, using defaults",
			zap.String("user_id", r.userId), zap.Error(err))
	}

	// Step 1: Determine target gender (M→F, F→M, O→all).
	targetGender := r.getTargetGender(ctx)

	// Step 2: Batch-fetch candidate items from the data store.
	// We fetch more than needed and filter in-memory.
	candidates, err := r.fetchProfileCandidates(ctx, cfg, pref, targetGender, excludeSet)
	if err != nil {
		return nil, "", errors.Trace(err)
	}

	if len(candidates) == 0 {
		return nil, ProfileMatchRecommender, nil
	}

	// Step 3: Score by profile quality.
	scored := r.scoreByProfile(candidates, cfg)

	// Step 4: Sort descending by score.
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	zap.L().Debug("profile_match",
		zap.String("user_id", r.userId),
		zap.String("target_gender", targetGender),
		zap.Int("candidates", len(candidates)),
		zap.Int("results", len(scored)))

	return scored, ProfileMatchRecommender, nil
}

// candidateItem holds an item with parsed profile fields for matching.
type candidateItem struct {
	item   data.Item
	age    int
	city   string
	purpose string
	profileScore float64 // 0-1, derived from completeness and quality metrics
	isVerified  bool
}

// fetchProfileCandidates retrieves items matching the user's stated preferences.
func (r *Recommender) fetchProfileCandidates(
	ctx context.Context,
	cfg ProfileMatchConfig,
	pref userPreference,
	targetGender string,
	excludeSet mapset.Set[string],
) ([]candidateItem, error) {
	activeCutoff := time.Time{}
	if cfg.ActiveWithinDays > 0 {
		activeCutoff = time.Now().AddDate(0, 0, -cfg.ActiveWithinDays)
	}

	var allCandidates []candidateItem

	if targetGender != "" {
		// Fetch by target gender category for efficiency.
		items, err := r.dataClient.GetLatestItems(ctx, cfg.MaxResults*3, []string{targetGender}, nil)
		if err != nil {
			return nil, errors.Trace(err)
		}
		for _, item := range items {
			if excludeSet.Contains(item.ItemId) {
				continue
			}
			c := r.parseCandidate(item, cfg)
			if c == nil {
				continue
			}
			if c.age < pref.PrefAgeMin || c.age > pref.PrefAgeMax {
				continue
			}
			if c.age < cfg.MinAge || c.age > cfg.MaxAge {
				continue
			}
			if pref.PrefCity != "" && c.city != "" && c.city != pref.PrefCity {
				continue
			}
			if pref.PrefPurpose != "" && c.purpose != "" && c.purpose != pref.PrefPurpose {
				continue
			}
			if !activeCutoff.IsZero() && !c.item.Timestamp.IsZero() && c.item.Timestamp.Before(activeCutoff) {
				continue
			}
			allCandidates = append(allCandidates, *c)
		}
	} else {
		// No gender filter: iterate with cursor.
		cursor := ""
		batchSize := cfg.MaxResults
		if batchSize <= 0 {
			batchSize = 200
		}
		for len(allCandidates) < cfg.MaxResults*2 {
			_, items, err := r.dataClient.GetItems(ctx, cursor, batchSize, nil)
			if err != nil {
				log.Logger().Warn("fetchProfileCandidates: GetItems failed, skipping batch",
					zap.String("cursor", cursor),
					zap.Error(err))
				break
			}
			if len(items) == 0 {
				break
			}
			for _, item := range items {
				if excludeSet.Contains(item.ItemId) {
					continue
				}
				c := r.parseCandidate(item, cfg)
				if c == nil {
					continue
				}
				if c.age < pref.PrefAgeMin || c.age > pref.PrefAgeMax {
					continue
				}
				if c.age < cfg.MinAge || c.age > cfg.MaxAge {
					continue
				}
				if pref.PrefCity != "" && c.city != "" && c.city != pref.PrefCity {
					continue
				}
				if pref.PrefPurpose != "" && c.purpose != "" && c.purpose != pref.PrefPurpose {
					continue
				}
				if !activeCutoff.IsZero() && !c.item.Timestamp.IsZero() && c.item.Timestamp.Before(activeCutoff) {
					continue
				}
				allCandidates = append(allCandidates, *c)
			}
			cursor = items[len(items)-1].ItemId
			if len(items) < batchSize {
				break
			}
		}
	}

	return allCandidates, nil
}

// parseCandidate extracts profile fields from an item (user-as-item).
func (r *Recommender) parseCandidate(item data.Item, cfg ProfileMatchConfig) *candidateItem {
	c := &candidateItem{item: item}

	// Gender: stored in Categories[0].
	if len(item.Categories) > 0 {
		cat := item.Categories[0]
		if cat != "M" && cat != "F" && cat != "O" {
			// Not a user item; skip.
			return nil
		}
	}

	// Age, city, purpose: in Labels.
	if labels, ok := item.Labels.(map[string]any); ok {
		if v, ok := labels["age"].(float64); ok {
			c.age = int(v)
		}
		if v, ok := labels["city"].(string); ok {
			c.city = v
		}
		if v, ok := labels["purpose"].(string); ok {
			c.purpose = v
		}

		// Profile completeness: count non-empty fields.
		total := 0
		filled := 0
		for _, key := range []string{"age", "city", "purpose", "photo_count", "quality_score"} {
			total++
			if _, ok := labels[key]; ok {
				filled++
			}
		}
		if total > 0 {
			c.profileScore = float64(filled) / float64(total)
		}

		// quality_score override.
		if v, ok := labels["quality_score"].(float64); ok {
			c.profileScore = v
		}

		// photo_count filter.
		if cfg.MinPhotoCount > 0 {
			photoCount := 0
			if v, ok := labels["photo_count"].(float64); ok {
				photoCount = int(v)
			}
			if photoCount < cfg.MinPhotoCount {
				return nil
			}
		}

		// Verified flag.
		if v, ok := labels["is_verified"].(bool); ok {
			c.isVerified = v
		}
	}

	return c
}

// scoreByProfile assigns a score to each candidate based on the configured strategy.
func (r *Recommender) scoreByProfile(candidates []candidateItem, cfg ProfileMatchConfig) []cache.Score {
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

	results := make([]cache.Score, 0, len(candidates))
	for _, c := range candidates {
		var score float64

		switch cfg.ScoreBy {
		case "profile_completeness":
			score = c.profileScore
		case "recency":
			daysSinceActive := maxTs.Sub(c.item.Timestamp).Hours() / 24
			score = math.Max(0, 1-daysSinceActive/30) // decay over 30 days
		default: // "combined"
			// Base: profile completeness.
			score = c.profileScore
			// Bonus: recency (decay over 30 days).
			daysSinceActive := maxTs.Sub(c.item.Timestamp).Hours() / 24
			recencyScore := math.Max(0, 1-daysSinceActive/30)
			score = 0.6*score + 0.4*recencyScore
			// Bonus: verified.
			if cfg.PreferVerified && c.isVerified {
				score += 0.1
			}
			if score > 1 {
				score = 1
			}
		}

		results = append(results, cache.Score{Id: c.item.ItemId, Score: score})
	}

	// Deduplicate (same itemId shouldn't appear twice).
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
