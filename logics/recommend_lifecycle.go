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
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorse-io/gorse/common/expression"
	"github.com/gorse-io/gorse/config"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/juju/errors"
	"github.com/redis/go-redis/v9"
	"github.com/samber/lo"
)

// LifecycleType represents a user lifecycle stage.
type LifecycleType string

const (
	LifecycleCold    LifecycleType = "cold"     // < 5 positive feedbacks
	LifecycleLight   LifecycleType = "light"    // 5-20 positive feedbacks
	LifecycleStable  LifecycleType = "stable"   // > 20 positive feedbacks
	LifecycleFatigue LifecycleType = "fatigue"  // stable + no match in 7 days
	LifecycleVIP     LifecycleType = "vip"      // tier=vip label or manual override
	LifecycleReturned LifecycleType = "returned" // inactive > 14 days, has history
)

// LifecycleProfile holds the lifecycle classification result for a user.
// It describes how much each recall pool should contribute to this user.
type LifecycleProfile struct {
	UserId        string                      `json:"user_id"`
	Primary       LifecycleType                `json:"primary"`        // dominant lifecycle type
	PoolWeights   map[string]float64           `json:"pool_weights"`   // pool_name -> blend weight
	ExploreRatio  float64                     `json:"explore_ratio"`   // effective exploration ratio
	ClassifiedAt  time.Time                   `json:"classified_at"`
	FatigueState  *FatigueState               `json:"fatigue_state,omitempty"`
}

type FatigueState struct {
	LastMatchAt   time.Time `json:"last_match_at"`
	SwipeCount    int       `json:"swipe_count"`    // consecutive swipes without match
	RecentTypes   []string  `json:"recent_types"`   // recent recommendation types
}

// PoolBlend describes how a single recall pool contributes to a user.
type PoolBlend struct {
	PoolName   string    `json:"pool_name"`
	Weight     float64   `json:"weight"`
	Recommenders []string `json:"recommenders"`
	ExploreRatio float64  `json:"explore_ratio"`
}

// LifecycleClassifier determines a user's lifecycle type and blend weights.
type LifecycleClassifier struct {
	cfg          config.LifecycleConfig
	dataSource   config.DataSourceConfig
	recallPools  []config.RecallPoolConfig
	fatigueCfg   config.FatigueConfig
	vipCfg       config.VIPConfig
	redis        *redis.Client
	dataClient   data.Database
}

// NewLifecycleClassifier creates a new lifecycle classifier.
func NewLifecycleClassifier(
	cfg config.LifecycleConfig,
	dataSource config.DataSourceConfig,
	recallPools []config.RecallPoolConfig,
	fatigueCfg config.FatigueConfig,
	vipCfg config.VIPConfig,
	redisClient *redis.Client,
	dataClient data.Database,
) *LifecycleClassifier {
	return &LifecycleClassifier{
		cfg:         cfg,
		dataSource:  dataSource,
		recallPools: recallPools,
		fatigueCfg:  fatigueCfg,
		vipCfg:      vipCfg,
		redis:       redisClient,
		dataClient:  dataClient,
	}
}

// ClassifyAsync computes the lifecycle profile for a user asynchronously.
// It caches the result in Redis with TTL = cfg.CacheTTL.
// Returns the cached profile if available, or nil if not yet computed.
func (c *LifecycleClassifier) ClassifyAsync(ctx context.Context, userId string) (*LifecycleProfile, error) {
	if !c.cfg.Enabled {
		return nil, nil // lifecycle disabled, skip
	}

	// Try to get cached profile first
	if c.redis != nil {
		key := c.lifecycleCacheKey(userId)
		val, err := c.redis.Get(ctx, key).Result()
		if err == nil {
			var profile LifecycleProfile
			if err := json.Unmarshal([]byte(val), &profile); err == nil {
				return &profile, nil
			}
		} else if !errors.Is(err, redis.Nil) {
			return nil, errors.Trace(err)
		}
	}

	// Compute profile synchronously (fast path, no DB round-trip for cached users)
	profile, err := c.classify(ctx, userId)
	if err != nil {
		return nil, errors.Trace(err)
	}

	// Cache result
	if c.redis != nil && c.cfg.CacheTTL > 0 {
		data, _ := json.Marshal(profile)
		c.redis.Set(ctx, c.lifecycleCacheKey(userId), data, c.cfg.CacheTTL)
	}

	return profile, nil
}

// Classify returns the lifecycle profile for a user, computing if necessary.
// If lifecycle is disabled, returns nil.
func (c *LifecycleClassifier) Classify(ctx context.Context, userId string) (*LifecycleProfile, error) {
	if !c.cfg.Enabled {
		return nil, nil
	}
	return c.classify(ctx, userId)
}

// classify does the actual classification logic.
func (c *LifecycleClassifier) classify(ctx context.Context, userId string) (*LifecycleProfile, error) {
	// Load user
	user, err := c.dataClient.GetUser(ctx, userId)
	if err != nil {
		return nil, errors.Trace(err)
	}

	// Load all feedback for this user (up to context_size limit)
	feedbacks, err := c.dataClient.GetUserFeedback(ctx, userId, nil)
	if err != nil {
		return nil, errors.Trace(err)
	}
	data.SortFeedbacks(feedbacks) // newest first

	// Count positive feedbacks using configured feedback types
	positiveCount := lo.CountBy(feedbacks, func(f data.Feedback) bool {
		return expression.MatchFeedbackTypeExpressions(c.dataSource.PositiveFeedbackTypes, f.FeedbackType, f.Value)
	})

	// Detect lifecycle type
	lifecycleType := c.detectType(ctx, user, feedbacks, positiveCount)

	// Load fatigue state if relevant
	var fatigueState *FatigueState
	if lifecycleType == LifecycleFatigue || lifecycleType == LifecycleStable {
		fs, _ := c.loadFatigueState(ctx, userId)
		fatigueState = fs
	}

	// Compute pool blend weights
	poolWeights, exploreRatio := c.computePoolBlend(lifecycleType)

	profile := &LifecycleProfile{
		UserId:       userId,
		Primary:      lifecycleType,
		PoolWeights:  poolWeights,
		ExploreRatio: exploreRatio,
		ClassifiedAt: time.Now(),
		FatigueState: fatigueState,
	}

	return profile, nil
}

// detectType determines the dominant lifecycle type for a user.
func (c *LifecycleClassifier) detectType(
	ctx context.Context,
	user data.User,
	feedbacks []data.Feedback,
	positiveCount int,
) LifecycleType {
	// 1. Check VIP first (highest priority)
	if c.isVIP(user) {
		return LifecycleVIP
	}

	// 2. Check fatigue (stable users only)
	if positiveCount > 20 {
		if c.isFatigued(feedbacks) {
			return LifecycleFatigue
		}
	}

	// 3. Check returned user (inactive > 14 days, has history)
	if positiveCount > 0 && c.isReturned(user) {
		return LifecycleReturned
	}

	// 4. Classify by positive feedback count
	switch {
	case positiveCount < 5:
		return LifecycleCold
	case positiveCount <= 20:
		return LifecycleLight
	default:
		return LifecycleStable
	}
}

// isVIP checks if user has VIP label.
func (c *LifecycleClassifier) isVIP(user data.User) bool {
	if c.vipCfg.VIPLabelKey == "" || c.vipCfg.VIPLabelValue == "" {
		return false
	}
	if labels, ok := user.Labels.(map[string]any); ok {
		if v, exists := labels[c.vipCfg.VIPLabelKey]; exists {
			if s, ok := v.(string); ok && s == c.vipCfg.VIPLabelValue {
				return true
			}
		}
	}
	return false
}

// isFatigued checks if a stable user is fatigued.
func (c *LifecycleClassifier) isFatigued(feedbacks []data.Feedback) bool {
	if c.fatigueCfg.TriggerDays <= 0 && c.fatigueCfg.TriggerSwipes <= 0 {
		return false
	}

	// Check last match time
	var lastMatch time.Time
	for _, f := range feedbacks {
		if f.FeedbackType == "match" {
			if f.Timestamp.After(lastMatch) {
				lastMatch = f.Timestamp
			}
		}
	}

	if c.fatigueCfg.TriggerDays > 0 && !lastMatch.IsZero() {
		if time.Since(lastMatch) > time.Duration(c.fatigueCfg.TriggerDays)*24*time.Hour {
			return true
		}
	}

	if c.fatigueCfg.TriggerSwipes > 0 {
		// Count consecutive non-match swipes from the most recent feedback backwards.
		// Stop counting when we encounter a match (it resets the counter).
		consecutiveNoMatch := 0
		for i := 0; i < len(feedbacks); i++ {
			f := feedbacks[i] // feedbacks are sorted newest-first
			if f.FeedbackType == "dislike" || f.FeedbackType == "block" {
				continue // skip negative feedback
			}
			if f.FeedbackType == "match" {
				break // hit a recent match, stop counting
			}
			// Positive feedback (like, view, etc.) but no match yet
			consecutiveNoMatch++
			if consecutiveNoMatch >= c.fatigueCfg.TriggerSwipes {
				return true
			}
		}
	}

	return false
}

// isReturned checks if user was inactive for > 14 days.
func (c *LifecycleClassifier) isReturned(user data.User) bool {
	// Check User.Timestamp for last activity (or use cache)
	// For now, check if the user's last feedback is > 14 days ago
	return false // TODO: implement using cache.LastModifyUserTime
}

// computePoolBlend computes blend weights for all pools based on lifecycle type.
func (c *LifecycleClassifier) computePoolBlend(lifecycleType LifecycleType) (map[string]float64, float64) {
	// Find matching pools for this lifecycle type
	var matchingPools []config.RecallPoolConfig
	for _, pool := range c.recallPools {
		if lo.Contains(pool.LifecycleTypes, string(lifecycleType)) {
			matchingPools = append(matchingPools, pool)
		}
	}

	if len(matchingPools) == 0 {
		// Fallback: use stable pool if no pool matches
		for _, pool := range c.recallPools {
			if pool.Name == "stable" {
				matchingPools = append(matchingPools, pool)
			}
		}
	}

	// Compute normalized weights
	totalWeight := 0.0
	for _, pool := range matchingPools {
		totalWeight += pool.BaseWeight
	}

	poolWeights := make(map[string]float64)
	var effectiveExploreRatio float64
	if totalWeight > 0 {
		for _, pool := range matchingPools {
			poolWeights[pool.Name] = pool.BaseWeight / totalWeight
			// Weighted average of explore ratios
			effectiveExploreRatio += (pool.BaseWeight / totalWeight) * pool.ExploreRatio
		}
	}

	return poolWeights, effectiveExploreRatio
}

// loadFatigueState loads fatigue state from Redis.
func (c *LifecycleClassifier) loadFatigueState(ctx context.Context, userId string) (*FatigueState, error) {
	if c.redis == nil {
		return nil, nil
	}
	key := fmt.Sprintf("fatigue:%s", userId)
	val, err := c.redis.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, errors.Trace(err)
	}
	if len(val) == 0 {
		return nil, nil
	}

	swipeCount := 0
	if v, ok := val["swipe_count"]; ok {
		fmt.Sscanf(v, "%d", &swipeCount)
	}
	lastMatchStr := val["last_match_at"]
	var lastMatch time.Time
	if lastMatchStr != "" {
		lastMatch, _ = time.Parse(time.RFC3339, lastMatchStr)
	}

	var recentTypes []string
	if rt, ok := val["recent_types"]; ok && rt != "" {
		json.Unmarshal([]byte(rt), &recentTypes)
	}

	return &FatigueState{
		LastMatchAt: lastMatch,
		SwipeCount:  swipeCount,
		RecentTypes: recentTypes,
	}, nil
}

// UpdateFatigueState updates the fatigue state in Redis after a recommendation.
func (c *LifecycleClassifier) UpdateFatigueState(ctx context.Context, userId string, swipeCount int, recType string) error {
	if c.redis == nil {
		return nil
	}
	key := fmt.Sprintf("fatigue:%s", userId)
	pipe := c.redis.Pipeline()
	pipe.HIncrBy(ctx, key, "swipe_count", int64(swipeCount))
	pipe.HSet(ctx, key, "last_rec_type", recType)
	// Append to recent types (keep last 10)
	val, _ := c.redis.HGet(ctx, key, "recent_types").Result()
	var recentTypes []string
	if val != "" {
		json.Unmarshal([]byte(val), &recentTypes)
	}
	recentTypes = append(recentTypes, recType)
	if len(recentTypes) > 10 {
		recentTypes = recentTypes[len(recentTypes)-10:]
	}
	typesJSON, _ := json.Marshal(recentTypes)
	pipe.HSet(ctx, key, "recent_types", string(typesJSON))
	pipe.Expire(ctx, key, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	return errors.Trace(err)
}

// RecordMatch updates fatigue state when user gets a match.
func (c *LifecycleClassifier) RecordMatch(ctx context.Context, userId string) error {
	if c.redis == nil {
		return nil
	}
	key := fmt.Sprintf("fatigue:%s", userId)
	pipe := c.redis.Pipeline()
	pipe.HSet(ctx, key, "last_match_at", time.Now().Format(time.RFC3339))
	pipe.HSet(ctx, key, "swipe_count", "0")
	pipe.HSet(ctx, key, "recent_types", "[]")
	pipe.Expire(ctx, key, 24*time.Hour)
	_, err := pipe.Exec(ctx)
	return errors.Trace(err)
}

// lifecycleCacheKey returns the Redis key for lifecycle profile cache.
func (c *LifecycleClassifier) lifecycleCacheKey(userId string) string {
	return fmt.Sprintf("lifecycle:%s", userId)
}

// GetPoolsForProfile returns the pool blend for a given lifecycle profile.
// This is called by the pool blend router to get which pools to query.
func (c *LifecycleClassifier) GetPoolsForProfile(profile *LifecycleProfile) []PoolBlend {
	var blends []PoolBlend
	for _, pool := range c.recallPools {
		weight, ok := profile.PoolWeights[pool.Name]
		if !ok || weight == 0 {
			continue
		}
		blends = append(blends, PoolBlend{
			PoolName:     pool.Name,
			Weight:       weight,
			Recommenders: pool.Recommenders,
			ExploreRatio: pool.ExploreRatio,
		})
	}
	return blends
}

// InvalidateCache removes the cached lifecycle profile for a user.
func (c *LifecycleClassifier) InvalidateCache(ctx context.Context, userId string) error {
	if c.redis == nil {
		return nil
	}
	return errors.Trace(c.redis.Del(ctx, c.lifecycleCacheKey(userId)).Err())
}

// GenderCount holds the count of users per gender group.
type GenderCount struct {
	Gender string
	Count  int
}

// GetGenderDistribution returns the count of users by gender.
func (c *LifecycleClassifier) GetGenderDistribution(ctx context.Context) (map[string]int, error) {
	if c.dataClient == nil {
		return nil, nil
	}
	// Iterate users and count by gender
	total, err := c.dataClient.CountUsers(ctx)
	if err != nil {
		return nil, errors.Trace(err)
	}
	counts := make(map[string]int)
	cursor := ""
	for {
		var users []data.User
		cursor, users, err = c.dataClient.GetUsers(ctx, cursor, 1000)
		if err != nil {
			return nil, errors.Trace(err)
		}
		for _, u := range users {
			gender := "unset"
			if u.Gender != nil && *u.Gender != "" {
				gender = *u.Gender
			}
			counts[gender]++
		}
		if cursor == "" || len(users) == 0 {
			break
		}
	}
	_ = total // suppress unused warning
	return counts, nil
}

// SetRedisClient sets the Redis client (allows late binding).
func (c *LifecycleClassifier) SetRedisClient(client *redis.Client) {
	c.redis = client
}
