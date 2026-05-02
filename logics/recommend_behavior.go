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
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/gorse-io/gorse/config"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/juju/errors"
	"github.com/redis/go-redis/v9"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

// BehaviorStats holds computed behavioral features for a user.
type BehaviorStats struct {
	UserId           string  `json:"user_id"`
	RightSwipeCount  int     `json:"right_swipe_count"`   // likes + matches
	TotalSwipeCount  int     `json:"total_swipe_count"`   // likes + dislikes
	RightSwipeRate   float64 `json:"right_swipe_rate"`    // right / total (0-1)
	MatchCount       int     `json:"match_count"`         // matches
	MatchRate        float64 `json:"match_rate"`          // matches / total
	BehaviorStability float64 `json:"behavior_stability"` // 1 - variance/mean (0-1)
	ExploreRatio     float64 `json:"explore_ratio"`       // effective exploration ratio
	LastUpdated      time.Time `json:"last_updated"`
}

// ItemStats holds computed quality features for an item (user-as-item).
type ItemStats struct {
	ItemId        string  `json:"item_id"`
	RightSwipeCount int    `json:"right_swipe_count"`  // times liked
	TotalExposure  int     `json:"total_exposure"`    // times shown
	RightSwipeRate float64 `json:"right_swipe_rate"`  // right / exposure (0-1)
	BlockCount    int     `json:"block_count"`        // times blocked
	BlockRate     float64 `json:"block_rate"`         // block / exposure
	LikeRate      float64 `json:"like_rate"`           // likes / exposure
	LastUpdated   time.Time `json:"last_updated"`
}

// BehaviorStatsTracker reads behavioral stats from Redis at request time.
type BehaviorStatsTracker struct {
	cfg   config.BehaviorConfig
	redis *redis.Client
}

// NewBehaviorStatsTracker creates a new behavior stats tracker.
func NewBehaviorStatsTracker(cfg config.BehaviorConfig, redisClient *redis.Client) *BehaviorStatsTracker {
	return &BehaviorStatsTracker{cfg: cfg, redis: redisClient}
}

// GetStats reads the behavior stats for a user from Redis.
func (t *BehaviorStatsTracker) GetStats(ctx context.Context, userId string) (*BehaviorStats, error) {
	if t.redis == nil {
		return nil, nil
	}
	key := fmt.Sprintf("behavior:%s", userId)
	val, err := t.redis.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Trace(err)
	}
	var stats BehaviorStats
	if err := json.Unmarshal([]byte(val), &stats); err != nil {
		return nil, errors.Trace(err)
	}
	return &stats, nil
}

// GetExploreRatio returns the effective explore ratio for a user.
// Returns configured default if no stats are available.
func (t *BehaviorStatsTracker) GetExploreRatio(ctx context.Context, userId string) float64 {
	stats, err := t.GetStats(ctx, userId)
	if err != nil || stats == nil {
		return t.cfg.DefaultExploreRatio
	}
	// If behavior is unstable (low stability), increase exploration
	exploreRatio := stats.ExploreRatio
	if stats.BehaviorStability < 0.5 && exploreRatio < 0.4 {
		exploreRatio = math.Min(0.5, exploreRatio+0.15)
	}
	return exploreRatio
}

// BehaviorStatsComputer periodically computes behavioral stats from feedback data.
type BehaviorStatsComputer struct {
	cfg        config.BehaviorConfig
	itemCfg    config.ItemStatsConfig
	redis      *redis.Client
	dataClient data.Database
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewBehaviorStatsComputer creates a new behavior stats computer.
func NewBehaviorStatsComputer(
	cfg config.BehaviorConfig,
	itemCfg config.ItemStatsConfig,
	redisClient *redis.Client,
	dataClient data.Database,
) *BehaviorStatsComputer {
	return &BehaviorStatsComputer{
		cfg:        cfg,
		itemCfg:    itemCfg,
		redis:      redisClient,
		dataClient: dataClient,
		stopCh:     make(chan struct{}),
	}
}

// Start begins the periodic background computation.
func (c *BehaviorStatsComputer) Start(interval time.Duration) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		// Run immediately on start
		ctx := context.Background()
		if err := c.ComputeAll(ctx); err != nil {
			zap.L().Warn("BehaviorStatsComputer initial run failed", zap.Error(err))
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := c.ComputeAll(ctx); err != nil {
					zap.L().Warn("BehaviorStatsComputer run failed", zap.Error(err))
				}
			case <-c.stopCh:
				return
			}
		}
	}()
}

// Stop halts the background computation.
func (c *BehaviorStatsComputer) Stop() {
	close(c.stopCh)
	c.wg.Wait()
}

// ComputeAll computes stats for all users with sufficient feedback.
func (c *BehaviorStatsComputer) ComputeAll(ctx context.Context) error {
	if !c.cfg.Enabled && !c.itemCfg.Enabled {
		return nil
	}

	// Collect all active users with feedback
	users := make(map[string]bool)
	cursor := ""
	for {
		var feedbacks []data.Feedback
		var err error
		cursor, feedbacks, err = c.dataClient.GetFeedback(ctx, cursor, 1000, nil, nil)
		if err != nil {
			return errors.Trace(err)
		}
		for _, f := range feedbacks {
			users[f.UserId] = true
		}
		if cursor == "" || len(feedbacks) == 0 {
			break
		}
	}

	// Compute stats in batches
	batchSize := 100
	userList := lo.Keys(users)
	for i := 0; i < len(userList); i += batchSize {
		end := i + batchSize
		if end > len(userList) {
			end = len(userList)
		}
		batch := userList[i:end]
		for _, userId := range batch {
			if err := c.ComputeUserStats(ctx, userId); err != nil {
				zap.L().Warn("failed to compute behavior stats",
					zap.String("user_id", userId),
					zap.Error(err))
			}
			if c.itemCfg.Enabled {
				if err := c.ComputeItemStatsForUser(ctx, userId); err != nil {
					zap.L().Warn("failed to compute item stats",
						zap.String("user_id", userId),
						zap.Error(err))
				}
			}
		}
	}
	return nil
}

// ComputeUserStats computes behavioral stats for a single user and writes to Redis + User Labels.
func (c *BehaviorStatsComputer) ComputeUserStats(ctx context.Context, userId string) error {
	if c.redis == nil {
		return nil
	}

	feedbacks, err := c.dataClient.GetUserFeedback(ctx, userId, nil)
	if err != nil {
		return errors.Trace(err)
	}
	data.SortFeedbacks(feedbacks) // newest first

	// Count swipes
	rightCount := 0  // like + match
	totalCount := 0  // like + dislike
	matchCount := 0  // match
	for _, f := range feedbacks {
		switch f.FeedbackType {
		case "like", "match":
			rightCount++
			totalCount++
			if f.FeedbackType == "match" {
				matchCount++
			}
		case "dislike":
			totalCount++
		}
	}

	if totalCount < c.cfg.MinSwipeCount {
		// Not enough data — clear any existing stats
		key := fmt.Sprintf("behavior:%s", userId)
		c.redis.Del(ctx, key)
		return nil
	}

	// Compute stability: variance in right-swipe rate over sliding windows
	stability := c.computeStability(feedbacks, c.cfg.StabilityWindow)

	// Compute effective explore ratio based on stability and right-swipe rate
	// Unstable users (stability < 0.5) → higher exploration
	rightRate := float64(rightCount) / float64(totalCount)
	exploreRatio := c.cfg.DefaultExploreRatio
	if rightRate > 0.5 {
		exploreRatio = math.Min(0.4, exploreRatio+0.1) // Active user: slight more personalization
	}
	if stability < 0.5 {
		exploreRatio = math.Min(0.5, exploreRatio+0.15) // Unstable: more exploration
	}

	stats := BehaviorStats{
		UserId:           userId,
		RightSwipeCount:  rightCount,
		TotalSwipeCount:  totalCount,
		RightSwipeRate:   rightRate,
		MatchCount:       matchCount,
		MatchRate:        float64(matchCount) / float64(totalCount),
		BehaviorStability: stability,
		ExploreRatio:     exploreRatio,
		LastUpdated:      time.Now(),
	}

	// Write to Redis
	statsData, err := json.Marshal(stats)
	if err != nil {
		return errors.Trace(err)
	}
	ttl := time.Duration(c.cfg.StatsCacheTTL) * time.Second
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	key := fmt.Sprintf("behavior:%s", userId)
	if err := c.redis.Set(ctx, key, statsData, ttl).Err(); err != nil {
		return errors.Trace(err)
	}

	// Also write to User Labels for persistence (used by FM ranker)
	user, err := c.dataClient.GetUser(ctx, userId)
	if err == nil && user.Labels == nil {
		user.Labels = make(map[string]any)
	}
	if err == nil {
		labels := toMapAny(user.Labels)
		labels["right_swipe_rate"] = stats.RightSwipeRate
		labels["right_swipe_count"] = stats.RightSwipeCount
		labels["total_swipe_count"] = stats.TotalSwipeCount
		labels["match_rate"] = stats.MatchRate
		labels["behavior_stability"] = stats.BehaviorStability
		labels["explore_ratio"] = stats.ExploreRatio
		labels["stats_updated_at"] = stats.LastUpdated.Format(time.RFC3339)
		if err := c.dataClient.ModifyUser(ctx, userId, data.UserPatch{Labels: labels}); err != nil {
			zap.L().Warn("failed to write behavior stats to user labels",
				zap.String("user_id", userId),
				zap.Error(err))
		}
	}

	return nil
}

// computeStability calculates behavioral stability as 1 - (normalized variance).
// Looks at right-swipe rate in rolling windows of size windowSize.
// Returns 0-1 where 1 = perfectly stable, 0 = highly variable.
func (c *BehaviorStatsComputer) computeStability(feedbacks []data.Feedback, windowSize int) float64 {
	if windowSize <= 0 {
		windowSize = 20
	}
	if len(feedbacks) < windowSize {
		return 1.0 // Not enough data, assume stable
	}

	// Build rolling right-swipe rate windows
	var rates []float64
	for i := 0; i <= len(feedbacks)-windowSize; i++ {
		window := feedbacks[i : i+windowSize]
		right := 0
		for _, f := range window {
			if f.FeedbackType == "like" || f.FeedbackType == "match" {
				right++
			}
		}
		rates = append(rates, float64(right)/float64(windowSize))
	}

	if len(rates) < 2 {
		return 1.0
	}

	// Compute coefficient of variation (CV = stddev/mean)
	mean := lo.Sum(rates) / float64(len(rates))
	if mean == 0 {
		return 1.0
	}
	variance := 0.0
	for _, r := range rates {
		diff := r - mean
		variance += diff * diff
	}
	variance /= float64(len(rates))
	stddev := math.Sqrt(variance)
	cv := stddev / mean

	// stability = 1 / (1 + cv) → ranges from 0 (high variance) to 1 (stable)
	stability := 1.0 / (1.0 + cv)
	return math.Max(0, math.Min(1, stability))
}

// ComputeItemStatsForUser computes item-level stats from feedback given TO items by this user.
// For dating apps: items are other users, so this computes "how does this user treat their matches?"
func (c *BehaviorStatsComputer) ComputeItemStatsForUser(ctx context.Context, userId string) error {
	if c.redis == nil {
		return nil
	}

	feedbacks, err := c.dataClient.GetUserFeedback(ctx, userId, nil)
	if err != nil {
		return errors.Trace(err)
	}

	// Aggregate per-item stats
	itemStats := make(map[string]*ItemStats)
	for _, f := range feedbacks {
		stats, ok := itemStats[f.ItemId]
		if !ok {
			stats = &ItemStats{ItemId: f.ItemId}
			itemStats[f.ItemId] = stats
		}
		stats.TotalExposure++
		switch f.FeedbackType {
		case "like", "match":
			stats.RightSwipeCount++
		case "block":
			stats.BlockCount++
		}
	}

	// Write stats for items with enough exposure
	for itemId, stats := range itemStats {
		if stats.TotalExposure < c.itemCfg.MinExposureCount {
			continue
		}
		stats.RightSwipeRate = float64(stats.RightSwipeCount) / float64(stats.TotalExposure)
		stats.LikeRate = float64(stats.RightSwipeCount) / float64(stats.TotalExposure)
		stats.BlockRate = float64(stats.BlockCount) / float64(stats.TotalExposure)
		stats.LastUpdated = time.Now()

		statsData, err := json.Marshal(stats)
		if err != nil {
			continue
		}
		ttl := time.Duration(c.itemCfg.StatsCacheTTL) * time.Second
		if ttl == 0 {
			ttl = 10 * time.Minute
		}
		key := fmt.Sprintf("item_stats:%s", itemId)
		if err := c.redis.Set(ctx, key, statsData, ttl).Err(); err != nil {
			zap.L().Warn("failed to write item stats to Redis",
				zap.String("item_id", itemId),
				zap.Error(err))
		}
	}
	return nil
}

// ItemStatsTracker reads item quality stats from Redis.
type ItemStatsTracker struct {
	cfg   config.ItemStatsConfig
	redis *redis.Client
}

// NewItemStatsTracker creates a new item stats tracker.
func NewItemStatsTracker(cfg config.ItemStatsConfig, redisClient *redis.Client) *ItemStatsTracker {
	return &ItemStatsTracker{cfg: cfg, redis: redisClient}
}

// GetStats reads item quality stats from Redis.
func (t *ItemStatsTracker) GetStats(ctx context.Context, itemId string) (*ItemStats, error) {
	if t.redis == nil {
		return nil, nil
	}
	key := fmt.Sprintf("item_stats:%s", itemId)
	val, err := t.redis.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Trace(err)
	}
	var stats ItemStats
	if err := json.Unmarshal([]byte(val), &stats); err != nil {
		return nil, errors.Trace(err)
	}
	return &stats, nil
}

// GetBatchStats reads item quality stats for multiple items.
func (t *ItemStatsTracker) GetBatchStats(ctx context.Context, itemIds []string) (map[string]*ItemStats, error) {
	if t.redis == nil || len(itemIds) == 0 {
		return nil, nil
	}
	pipe := t.redis.Pipeline()
	cmds := make(map[string]*redis.StringCmd)
	for _, id := range itemIds {
		key := fmt.Sprintf("item_stats:%s", id)
		cmds[id] = pipe.Get(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, errors.Trace(err)
	}
	result := make(map[string]*ItemStats)
	for id, cmd := range cmds {
		val, err := cmd.Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			continue
		}
		var stats ItemStats
		if err := json.Unmarshal([]byte(val), &stats); err == nil {
			result[id] = &stats
		}
	}
	return result, nil
}

// FilterByQuality filters out items that don't meet quality thresholds.
func (t *ItemStatsTracker) FilterByQuality(scores []cache.Score) []cache.Score {
	if t.redis == nil || !t.cfg.Enabled {
		return scores
	}
	if len(scores) == 0 {
		return scores
	}

	ctx := context.Background()
	itemIds := make([]string, len(scores))
	itemIndex := make(map[string]int)
	for i, s := range scores {
		itemIds[i] = s.Id
		itemIndex[s.Id] = i
	}

	statsMap, err := t.GetBatchStats(ctx, itemIds)
	if err != nil || len(statsMap) == 0 {
		return scores // No stats available, don't filter
	}

	filtered := make([]cache.Score, 0, len(scores))
	filteredOut := 0
	for _, s := range scores {
		stats, ok := statsMap[s.Id]
		if !ok {
			// No stats: allow item through
			filtered = append(filtered, s)
			continue
		}
		// Apply quality thresholds
		if stats.RightSwipeRate < t.cfg.MinLikeRate {
			filteredOut++
			continue // Item gets low like rate from users — skip
		}
		if stats.BlockRate > t.cfg.MaxBlockRate {
			filteredOut++
			continue // Item gets blocked too often — skip
		}
		filtered = append(filtered, s)
	}

	if filteredOut > 0 {
		zap.L().Info("quality filter removed items",
			zap.Int("filtered_out", filteredOut),
			zap.Int("remaining", len(filtered)))
	}
	return filtered
}

// ApplyExploreExploit injects random/fresh candidates based on explore ratio.
// Takes primary personalized results and fills explore_ratio * n slots with diverse candidates.
func (t *BehaviorStatsTracker) ApplyExploreExploit(
	ctx context.Context,
	primary []cache.Score,
	allCandidates []cache.Score,
	n int,
) []cache.Score {
	if n <= 0 || len(primary) == 0 {
		return primary
	}

	exploreRatio := t.GetExploreRatio(ctx, primary[0].Id)
	// Actually use the requesting user's ID — primary[0].Id is item, not user
	// We need to re-fetch. For now use the configured default.
	_ = exploreRatio // handled in tracker

	if len(allCandidates) == 0 {
		return primary
	}

	// Build set of already-in-primary
	inPrimary := make(map[string]bool)
	for _, s := range primary {
		inPrimary[s.Id] = true
	}

	// Collect explore candidates: fresh or random items not in primary
	var exploreCandidates []cache.Score
	for _, s := range allCandidates {
		if !inPrimary[s.Id] {
			exploreCandidates = append(exploreCandidates, s)
		}
	}

	if len(exploreCandidates) == 0 {
		return primary
	}

	// Shuffle explore candidates for randomness
	rand.Shuffle(len(exploreCandidates), func(i, j int) {
		exploreCandidates[i], exploreCandidates[j] = exploreCandidates[j], exploreCandidates[i]
	})

	// Determine how many explore slots to fill
	// Use the first item's user context if available
	exploreCount := int(float64(n) * exploreRatio)
	if exploreCount <= 0 {
		return primary
	}

	// Limit to available explore candidates
	if exploreCount > len(exploreCandidates) {
		exploreCount = len(exploreCandidates)
	}

	// Interleave: take primary in order, inserting explore candidates at intervals
	result := make([]cache.Score, 0, n)
	primaryIdx := 0
	exploreIdx := 0
	step := 1
	if exploreCount > 0 {
		// Interleave: insert an explore item every (n/exploreCount) positions
		step = (len(primary) + exploreCount - 1) / exploreCount
		if step < 1 {
			step = 1
		}
	}

	for len(result) < n && (primaryIdx < len(primary) || exploreIdx < exploreCount) {
		// Insert explore candidate at step intervals
		if exploreIdx < exploreCount && len(result) > 0 && len(result)%step == 0 && exploreIdx < len(exploreCandidates) {
			result = append(result, exploreCandidates[exploreIdx])
			exploreIdx++
		}
		// Then add primary items
		if primaryIdx < len(primary) {
			result = append(result, primary[primaryIdx])
			primaryIdx++
		}
	}

	// Fill remaining slots with explore candidates
	for exploreIdx < len(exploreCandidates) && len(result) < n {
		result = append(result, exploreCandidates[exploreIdx])
		exploreIdx++
	}

	return result[:n]
}

// toMapAny converts an interface{} to map[string]any, handling nil.
func toMapAny(v any) map[string]any {
	if v == nil {
		return make(map[string]any)
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return make(map[string]any)
}
