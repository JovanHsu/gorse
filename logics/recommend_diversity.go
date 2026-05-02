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
	"encoding/json"
	"fmt"
	"math"
	"sort"
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

// MMRReranker applies Maximum Marginal Relevance to diversify recommendations.
// It balances relevance (original score) with diversity (pairwise item distance).
// formula: score_i = relevance_i - λ * max_j(similarity(i, j))
type MMRReranker struct {
	cfg         config.MMRConfig
	redis       *redis.Client
	dataClient  data.Database
}

// MMRConfig holds diversity control parameters.
type MMRConfig struct {
	Enabled        bool    `mapstructure:"enabled"`
	Lambda         float64 `mapstructure:"lambda"`           // diversity weight (0-1), higher = more diverse
	WindowSize     int     `mapstructure:"window_size"`     // how many top items to check for similarity
	AgeDecay      float64 `mapstructure:"age_decay"`       // penalty for age similarity (0-1)
	GenderDecay   float64 `mapstructure:"gender_decay"`    // penalty for same gender (0-1)
}

// NewMMRReranker creates a new MMR reranker.
func NewMMRReranker(cfg config.MMRConfig, redisClient *redis.Client, dataClient data.Database) *MMRReranker {
	return &MMRReranker{cfg: cfg, redis: redisClient, dataClient: dataClient}
}

// Rerank applies MMR diversity to the scored items.
func (r *MMRReranker) Rerank(ctx context.Context, items []cache.Score) ([]cache.Score, error) {
	if !r.cfg.Enabled || len(items) <= 1 {
		return items, nil
	}

	windowSize := r.cfg.WindowSize
	if windowSize <= 0 {
		windowSize = 10
	}
	if windowSize > len(items) {
		windowSize = len(items)
	}

	// Load item metadata for similarity computation
	itemIds := lo.Map(items, func(s cache.Score, _ int) string { return s.Id })
	loadedItems, err := r.dataClient.BatchGetItems(ctx, itemIds, data.GetOptions{SkipHidden: true})
	if err != nil {
		return items, nil // Non-fatal, return original
	}
	itemMap := make(map[string]*data.Item)
	for i := range loadedItems {
		itemMap[loadedItems[i].ItemId] = &loadedItems[i]
	}

	lambda := r.cfg.Lambda
	if lambda <= 0 {
		lambda = 0.3
	}

	// MMR greedy selection: iteratively pick the item with best MMR score
	// Start from the highest-scoring items
	window := make([]cache.Score, 0, windowSize)
	result := make([]cache.Score, 0, len(items))

	// Items that have been processed
	processed := make(map[string]bool)

	// Precompute normalized scores
	maxScore := items[0].Score
	for _, item := range items {
		if item.Score > maxScore {
			maxScore = item.Score
		}
	}
	if maxScore == 0 {
		maxScore = 1.0
	}

	// MMR: keep picking until all items are processed
	remaining := make([]cache.Score, len(items))
	copy(remaining, items)
	resetProcessed := func() {
		for k := range processed {
			delete(processed, k)
		}
	}
	resetProcessed()

	for len(remaining) > 0 {
		bestIdx := -1
		bestScore := -math.MaxFloat64

		for i, candidate := range remaining {
			if processed[candidate.Id] {
				continue
			}

			// Relevance: normalized score
			relevance := candidate.Score / maxScore

			// Diversity penalty: max similarity to items already in result
			maxSim := 0.0
			for _, selected := range window {
				sim := r.itemSimilarity(itemMap[candidate.Id], itemMap[selected.Id])
				if sim > maxSim {
					maxSim = sim
				}
			}

			// MMR score = relevance - λ * max_similarity
			mmrScore := relevance - lambda*maxSim

			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}

		if bestIdx == -1 {
			break
		}

		selected := remaining[bestIdx]
		result = append(result, selected)
		processed[selected.Id] = true

		// Update window (keep last N selected items)
		window = append(window, selected)
		if len(window) > windowSize {
			window = window[len(window)-windowSize:]
		}

		// Remove from remaining
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	zap.L().Info("MMR reranking applied",
		zap.Int("input", len(items)),
		zap.Int("output", len(result)),
		zap.Float64("lambda", lambda))

	return result, nil
}

// itemSimilarity computes a 0-1 similarity between two items.
func (r *MMRReranker) itemSimilarity(a, b *data.Item) float64 {
	if a == nil || b == nil {
		return 0
	}
	sim := 0.0
	n := 0.0

	// Gender similarity (0 if same, 1 if different - we want to encourage diversity)
	if r.cfg.GenderDecay > 0 {
		genderA := ""
		genderB := ""
		if len(a.Categories) > 0 {
			genderA = a.Categories[0]
		}
		if len(b.Categories) > 0 {
			genderB = b.Categories[0]
		}
		// Different gender = diverse = good (low penalty). Same gender = similar = bad (high penalty)
		if genderA != "" && genderB != "" {
			if genderA == genderB {
				sim += r.cfg.GenderDecay
			}
			// Different gender gets 0 penalty (good for dating apps)
			n += r.cfg.GenderDecay
		}
	}

	// Age similarity (labels.age)
	if labelsA, ok := a.Labels.(map[string]any); ok {
		if labelsB, ok := b.Labels.(map[string]any); ok {
			if ageA, okA := labelsA["age"].(float64); okA {
				if ageB, okB := labelsB["age"].(float64); okB {
					// Penalize if ages are within 3 years
					ageDiff := math.Abs(ageA - ageB)
					if ageDiff < 3 {
						sim += r.cfg.AgeDecay * (1 - ageDiff/3)
					}
					n += r.cfg.AgeDecay
				}
			}
		}
	}

	// Timestamp similarity (recency)
	if !a.Timestamp.IsZero() && !b.Timestamp.IsZero() {
		daysDiff := math.Abs(a.Timestamp.Sub(b.Timestamp).Hours()) / 24
		if daysDiff < 30 {
			ageDecay := r.cfg.AgeDecay
			if ageDecay == 0 {
				ageDecay = 0.2
			}
			sim += ageDecay * (1 - daysDiff/30)
			n += ageDecay
		}
	}

	if n > 0 {
		return sim / n
	}
	return 0
}

// SuccessRateReranker boosts items with high historical like/match rates.
type SuccessRateReranker struct {
	cfg   config.SuccessRateConfig
	redis *redis.Client
}

// SuccessRateConfig controls success-rate based reranking.
type SuccessRateConfig struct {
	Enabled       bool    `mapstructure:"enabled"`
	RedisAddr    string  `mapstructure:"redis_addr"`
	RedisPassword string  `mapstructure:"redis_password"`
	MinExposures  int     `mapstructure:"min_exposures"`   // minimum exposures before boosting
	SuccessWeight float64 `mapstructure:"success_weight"`   // weight for success rate (0-1)
	MatchBoost   float64 `mapstructure:"match_boost"`      // boost multiplier for high match rate items
}

// NewSuccessRateReranker creates a new success-rate reranker.
func NewSuccessRateReranker(cfg config.SuccessRateConfig, redisClient *redis.Client) *SuccessRateReranker {
	return &SuccessRateReranker{cfg: cfg, redis: redisClient}
}

// Rerank reorders items by combining original score with historical success rate.
func (r *SuccessRateReranker) Rerank(ctx context.Context, items []cache.Score) ([]cache.Score, error) {
	if !r.cfg.Enabled || len(items) == 0 || r.redis == nil {
		return items, nil
	}

	itemIds := lo.Map(items, func(s cache.Score, _ int) string { return s.Id })

	// Batch read success rates from Redis
	pipe := r.redis.Pipeline()
	cmds := make(map[string]*redis.StringCmd)
	for _, id := range itemIds {
		key := fmt.Sprintf("item_success:%s", id)
		cmds[id] = pipe.Get(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return items, nil
	}

	// Find max original score for normalization
	maxScore := 0.0
	for _, s := range items {
		if s.Score > maxScore {
			maxScore = s.Score
		}
	}
	if maxScore == 0 {
		maxScore = 1.0
	}

	// Compute boosted scores
	boosted := make([]struct {
		score cache.Score
		value float64
	}, len(items))

	for i, item := range items {
		cmd := cmds[item.Id]
		var successRate float64
		if val, err := cmd.Result(); err == nil {
			fmt.Sscanf(val, "%f", &successRate)
		}

		// Combined score = original_score + success_weight * success_rate * max_score
		boost := r.cfg.SuccessWeight * successRate * maxScore
		boosted[i] = struct {
			score cache.Score
			value float64
		}{item, item.Score + boost}
	}

	// Sort by boosted score
	sort.Slice(boosted, func(i, j int) bool {
		return boosted[i].value > boosted[j].value
	})

	result := make([]cache.Score, len(boosted))
	for i, b := range boosted {
		result[i] = b.score
	}

	successCount := 0
	for _, b := range boosted {
		if b.value > b.score.Score {
			successCount++
		}
	}
	if successCount > 0 {
		zap.L().Info("success-rate reranking applied",
			zap.Int("boosted", successCount),
			zap.Int("total", len(items)))
	}

	return result, nil
}

// SessionFatigueTracker tracks fatigue within the current session.
// It increments a counter for each recommendation shown and resets on match.
type SessionFatigueTracker struct {
	cfg   config.SessionFatigueConfig
	redis *redis.Client
	mu    sync.RWMutex
	// Local in-memory state for this server instance
	localState map[string]*sessionState
}

type sessionState struct {
	swipeCount      int       // consecutive non-match swipes in this session
	lastSeenAt      time.Time
	recsShown      int        // recommendations shown in this session
	matchFound     bool       // match seen in this session
}

type SessionFatigueConfig struct {
	Enabled          bool    `mapstructure:"enabled"`
	RedisAddr       string  `mapstructure:"redis_addr"`
	RedisPassword   string  `mapstructure:"redis_password"`
	ResetOnMatch    bool    `mapstructure:"reset_on_match"`    // reset counter when match is recorded
	SwipeThreshold  int     `mapstructure:"swipe_threshold"`    // swipes before fatigue (default 15)
	RecsThreshold   int     `mapstructure:"recs_threshold"`     // recs before fatigue (default 20)
	DiversityBoost  float64 `mapstructure:"diversity_boost"`    // extra explore ratio when fatigued
}

// NewSessionFatigueTracker creates a new session fatigue tracker.
func NewSessionFatigueTracker(cfg config.SessionFatigueConfig, redisClient *redis.Client) *SessionFatigueTracker {
	return &SessionFatigueTracker{
		cfg:         cfg,
		redis:       redisClient,
		localState:  make(map[string]*sessionState),
	}
}

// RecordSwipe records a user swipe action in the current session.
func (t *SessionFatigueTracker) RecordSwipe(ctx context.Context, userId string, isMatch bool) error {
	if !t.cfg.Enabled {
		return nil
	}

	key := fmt.Sprintf("session_fatigue:%s", userId)

	if isMatch {
		// Reset on match
		if t.redis != nil {
			pipe := t.redis.Pipeline()
			pipe.HSet(ctx, key, "swipe_count", "0")
			pipe.HSet(ctx, key, "recs_shown", "0")
			pipe.HSet(ctx, key, "match_found", "1")
			pipe.Expire(ctx, key, 30*time.Minute)
			_, err := pipe.Exec(ctx)
			return err
		}
		t.mu.Lock()
		if s, ok := t.localState[userId]; ok {
			s.swipeCount = 0
			s.matchFound = true
		}
		t.mu.Unlock()
		return nil
	}

	// Increment swipe count
	if t.redis != nil {
		count, err := t.redis.HIncrBy(ctx, key, "swipe_count", 1).Result()
		if err != nil {
			return err
		}
		t.redis.HSet(ctx, key, "last_update", time.Now().Format(time.RFC3339))
		t.redis.Expire(ctx, key, 30*time.Minute)
		if count == 1 {
			// Initialize recs_shown from current value
			t.redis.HSetNX(ctx, key, "recs_shown", "0")
		}
	} else {
		t.mu.Lock()
		defer t.mu.Unlock()
		s := t.localState[userId]
		if s == nil {
			s = &sessionState{lastSeenAt: time.Now()}
			t.localState[userId] = s
		}
		s.swipeCount++
		s.lastSeenAt = time.Now()
	}
	return nil
}

// RecordRecShown records that recommendations were shown to a user.
func (t *SessionFatigueTracker) RecordRecShown(ctx context.Context, userId string, count int) error {
	if !t.cfg.Enabled || t.redis == nil {
		return nil
	}
	key := fmt.Sprintf("session_fatigue:%s", userId)
	_, err := t.redis.HIncrBy(ctx, key, "recs_shown", int64(count)).Result()
	if err != nil {
		return err
	}
	t.redis.HSet(ctx, key, "last_update", time.Now().Format(time.RFC3339))
	t.redis.Expire(ctx, key, 30*time.Minute)
	return nil
}

// IsFatigued returns true if the user is showing signs of fatigue in the current session.
func (t *SessionFatigueTracker) IsFatigued(ctx context.Context, userId string) (bool, float64) {
	if !t.cfg.Enabled {
		return false, 0
	}

	swipeThreshold := t.cfg.SwipeThreshold
	if swipeThreshold <= 0 {
		swipeThreshold = 15
	}
	recsThreshold := t.cfg.RecsThreshold
	if recsThreshold <= 0 {
		recsThreshold = 20
	}

	var swipeCount, recsShown int
	var matchFound bool

	if t.redis != nil {
		key := fmt.Sprintf("session_fatigue:%s", userId)
		val, err := t.redis.HGetAll(ctx, key).Result()
		if err != nil || len(val) == 0 {
			return false, 0
		}
		fmt.Sscanf(val["swipe_count"], "%d", &swipeCount)
		fmt.Sscanf(val["recs_shown"], "%d", &recsShown)
		matchFound = val["match_found"] == "1"
	} else {
		t.mu.RLock()
		s := t.localState[userId]
		t.mu.RUnlock()
		if s == nil {
			return false, 0
		}
		swipeCount = s.swipeCount
		recsShown = s.recsShown
		matchFound = s.matchFound
	}

	// Reset on match found
	if matchFound {
		return false, 0
	}

	fatigued := swipeCount >= swipeThreshold || recsShown >= recsThreshold

	// Extra diversity boost when fatigued
	diversityBoost := 0.0
	if fatigued {
		diversityBoost = t.cfg.DiversityBoost
		if diversityBoost <= 0 {
			diversityBoost = 0.15
		}
	}

	return fatigued, diversityBoost
}

// GetFatigueState returns the current fatigue state for a user.
func (t *SessionFatigueTracker) GetFatigueState(ctx context.Context, userId string) map[string]int {
	swipeThreshold := t.cfg.SwipeThreshold
	if swipeThreshold <= 0 {
		swipeThreshold = 15
	}
	recsThreshold := t.cfg.RecsThreshold
	if recsThreshold <= 0 {
		recsThreshold = 20
	}

	var swipeCount, recsShown int

	if t.redis != nil {
		key := fmt.Sprintf("session_fatigue:%s", userId)
		val, _ := t.redis.HGetAll(ctx, key).Result()
		if len(val) > 0 {
			fmt.Sscanf(val["swipe_count"], "%d", &swipeCount)
			fmt.Sscanf(val["recs_shown"], "%d", &recsShown)
		}
	} else {
		t.mu.RLock()
		defer t.mu.RUnlock()
		if s := t.localState[userId]; s != nil {
			swipeCount = s.swipeCount
			recsShown = s.recsShown
		}
	}

	return map[string]int{
		"swipe_count":    swipeCount,
		"recs_shown":     recsShown,
		"swipe_threshold": swipeThreshold,
		"recs_threshold": recsThreshold,
	}
}

// SuccessRateStats holds computed success statistics for an item.
type SuccessRateStats struct {
	ItemId         string  `json:"item_id"`
	LikeRate       float64 `json:"like_rate"`
	MatchRate      float64 `json:"match_rate"`
	TotalExposures int     `json:"total_exposures"`
	LastUpdated    time.Time `json:"last_updated"`
}

// ComputeSuccessRates computes like/match rates for items and writes to Redis.
// This should be run as a periodic background job.
type SuccessRateComputer struct {
	cfg       config.SuccessRateConfig
	itemCfg   config.ItemStatsConfig
	redis     *redis.Client
	dataClient data.Database
	stopCh    chan struct{}
}

func NewSuccessRateComputer(
	cfg config.SuccessRateConfig,
	itemCfg config.ItemStatsConfig,
	redisClient *redis.Client,
	dataClient data.Database,
) *SuccessRateComputer {
	return &SuccessRateComputer{
		cfg:       cfg,
		itemCfg:   itemCfg,
		redis:     redisClient,
		dataClient: dataClient,
		stopCh:    make(chan struct{}),
	}
}

func (c *SuccessRateComputer) Start(interval time.Duration) {
	go func() {
		ctx := context.Background()
		if err := c.ComputeAll(ctx); err != nil {
			zap.L().Warn("SuccessRateComputer initial run failed", zap.Error(err))
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := c.ComputeAll(ctx); err != nil {
					zap.L().Warn("SuccessRateComputer run failed", zap.Error(err))
				}
			case <-c.stopCh:
				return
			}
		}
	}()
}

func (c *SuccessRateComputer) Stop() {
	close(c.stopCh)
}

func (c *SuccessRateComputer) ComputeAll(ctx context.Context) error {
	if !c.cfg.Enabled && !c.itemCfg.Enabled {
		return nil
	}

	// Group feedback by item
	type itemAgg struct {
		likes   int
		matches int
		total   int
	}
	aggs := make(map[string]*itemAgg)

	offset := 0
	batchSize := 1000
	for {
		_, feedbacks, err := c.dataClient.GetFeedback(ctx, "", batchSize, nil, nil)
		if err != nil {
			return errors.Trace(err)
		}
		if len(feedbacks) == 0 {
			break
		}
		for _, f := range feedbacks {
			agg, ok := aggs[f.ItemId]
			if !ok {
				agg = &itemAgg{}
				aggs[f.ItemId] = agg
			}
			agg.total++
			switch f.FeedbackType {
			case "like":
				agg.likes++
			case "match":
				agg.matches++
			}
		}
		offset += len(feedbacks)
		if len(feedbacks) < batchSize {
			break
		}
	}

	// Write success rates to Redis
	pipe := c.redis.Pipeline()
	count := 0
	for itemId, agg := range aggs {
		if agg.total < c.cfg.MinExposures {
			continue
		}
		stats := SuccessRateStats{
			ItemId:         itemId,
			LikeRate:       float64(agg.likes) / float64(agg.total),
			MatchRate:      float64(agg.matches) / float64(agg.total),
			TotalExposures: agg.total,
			LastUpdated:    time.Now(),
		}
		data, _ := json.Marshal(stats)
		key := fmt.Sprintf("item_success:%s", itemId)
		pipe.Set(ctx, key, data, 24*time.Hour)
		count++
	}
	if count > 0 {
		_, err := pipe.Exec(ctx)
		if err != nil {
			return errors.Trace(err)
		}
		zap.L().Info("success rates computed",
			zap.Int("items_updated", count),
			zap.Int("total_items", len(aggs)))
	}
	return nil
}
