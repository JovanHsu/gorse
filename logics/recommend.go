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
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/gorse-io/gorse/common/expression"
	"github.com/gorse-io/gorse/common/heap"
	"github.com/gorse-io/gorse/common/log"
	"github.com/gorse-io/gorse/common/util"
	"github.com/gorse-io/gorse/config"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/juju/errors"
	"github.com/redis/go-redis/v9"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

const (
	LatestRecommender          = "latest"
	NonPersonalizedRecommender = "non-personalized/"
	ItemToItemRecommender      = "item-to-item/"
	UserToUserRecommender      = "user-to-user/"
	ExternalRecommender        = "external/"
	CollaborativeRecommender   = "collaborative"
	FatigueBreakerRecommender  = "fatigue_breaker"
	SocialGraphRecommender      = "social_graph"
	ProfileMatchRecommender     = "profile_match"
	VIPQualityPoolRecommender  = "vip_quality_pool"
	InterestBasedRecommender   = "interest_based"
	TemporalRecommender        = "temporal"
)

type Recommender struct {
	config      config.RecommendConfig
	cacheClient cache.Database
	dataClient  data.Database

	online       bool
	coldstart    bool
	userId       string
	userFeedback []data.Feedback
	categories   []string
	excludeSet   mapset.Set[string]

	// Lifecycle-aware recall support
	lifecycleClassifier *LifecycleClassifier
	lifecycleProfile   *LifecycleProfile

	// Supply/demand balance support (Phase 4)
	supplyDemandTracker *SupplyDemandTracker
	redisClient        *redis.Client

	// Behavior feature support (Phase 2)
	behaviorTracker *BehaviorStatsTracker
	itemStatsTracker *ItemStatsTracker

	// Phase 3: Diversity and session fatigue
	mmrReranker          *MMRReranker
	successRateReranker  *SuccessRateReranker
	sessionFatigueTracker *SessionFatigueTracker
}

type RecommenderFunc func(ctx context.Context) ([]cache.Score, string, error)

func NewRecommender(config config.RecommendConfig, cacheClient cache.Database, dataClient data.Database, online bool, userId string, categories []string) (*Recommender, error) {
	// Load user feedback
	userFeedback, err := dataClient.GetUserFeedback(context.Background(), userId, new(time.Now()))
	if err != nil {
		return nil, errors.Trace(err)
	}
	excludeSet := mapset.NewSet[string]()
	// Exclude the requesting user themselves from recommendations (for dating/social apps)
	excludeSet.Add(userId)
	coldstart := true
	for _, feedback := range userFeedback {
		// Negative feedback items should always be excluded (highest priority)
		if expression.MatchFeedbackTypeExpressions(config.DataSource.NegativeFeedbackTypes, feedback.FeedbackType, feedback.Value) {
			excludeSet.Add(feedback.ItemId)
		} else if !config.Replacement.EnableReplacement || !online {
			// Other feedback items are excluded unless replacement is enabled
			excludeSet.Add(feedback.ItemId)
		}
		if expression.MatchFeedbackTypeExpressions(config.DataSource.PositiveFeedbackTypes, feedback.FeedbackType, feedback.Value) {
			coldstart = false
		}
	}
	return &Recommender{
		config:       config,
		cacheClient:  cacheClient,
		dataClient:   dataClient,
		userId:       userId,
		userFeedback: userFeedback,
		online:       online,
		coldstart:    coldstart,
		categories:   categories,
		excludeSet:   excludeSet,
	}, nil
}

// NewRecommenderWithLifecycle creates a Recommender with lifecycle-aware pool blending.
// It classifies the user asynchronously and uses pool-based recall when lifecycle is enabled.
func NewRecommenderWithLifecycle(
	config config.RecommendConfig,
	cacheClient cache.Database,
	dataClient data.Database,
	redisClient *redis.Client,
	online bool,
	userId string,
	categories []string,
) (*Recommender, error) {
	recommender, err := NewRecommender(config, cacheClient, dataClient, online, userId, categories)
	if err != nil {
		return nil, errors.Trace(err)
	}
	// Set up lifecycle classifier if enabled
	if config.Lifecycle.Enabled && redisClient != nil {
		classifier := NewLifecycleClassifier(
			config.Lifecycle,
			config.DataSource,
			config.RecallPools,
			config.Fatigue,
			config.VIP,
			redisClient,
			dataClient,
		)
		recommender.lifecycleClassifier = classifier
		// Classify user synchronously (fast path with Redis cache)
		profile, err := classifier.Classify(context.Background(), userId)
		if err != nil {
			log.Logger().Warn("failed to classify user lifecycle",
				zap.String("user_id", userId),
				zap.Error(err))
			// Non-fatal: continue without lifecycle profile
		} else {
			recommender.lifecycleProfile = profile
		}
	}
	// Set up supply/demand tracker if enabled (Phase 4)
	if config.SupplyDemand.Enabled && redisClient != nil {
		recommender.redisClient = redisClient
		recommender.supplyDemandTracker = NewSupplyDemandTracker(
			config.SupplyDemand,
			redisClient,
			dataClient,
		)
	}
	// Set up behavior stats trackers if enabled (Phase 2)
	if redisClient != nil {
		if config.Behavior.Enabled {
			recommender.behaviorTracker = NewBehaviorStatsTracker(config.Behavior, redisClient)
		}
		if config.ItemStats.Enabled {
			recommender.itemStatsTracker = NewItemStatsTracker(config.ItemStats, redisClient)
		}
	}
	// Set up Phase 3 rerankers if enabled
	if redisClient != nil {
		if config.MMR.Enabled {
			recommender.mmrReranker = NewMMRReranker(config.MMR, redisClient, dataClient)
		}
		if config.SuccessRate.Enabled {
			recommender.successRateReranker = NewSuccessRateReranker(config.SuccessRate, redisClient)
		}
		if config.SessionFatigue.Enabled {
			recommender.sessionFatigueTracker = NewSessionFatigueTracker(config.SessionFatigue, redisClient)
		}
	}
	return recommender, nil
}

func (r *Recommender) ExcludeSet() mapset.Set[string] {
	return r.excludeSet
}

func (r *Recommender) UserFeedback() []data.Feedback {
	return r.userFeedback
}

func (r *Recommender) IsColdStart() bool {
	return r.coldstart
}

// getTargetGender returns the gender to filter recommendations for.
// M → recommend F, F → recommend M, O → no filter (return "").
func (r *Recommender) getTargetGender(ctx context.Context) string {
	user, err := r.dataClient.GetUser(ctx, r.userId)
	if err != nil {
		log.Logger().Warn("getTargetGender failed to get user",
			zap.String("user_id", r.userId),
			zap.Error(err))
		return ""
	}
	if user.Gender == nil {
		log.Logger().Warn("getTargetGender: user gender is nil",
			zap.String("user_id", r.userId))
		return ""
	}
	switch *user.Gender {
	case "M":
		return "F"
	case "F":
		return "M"
	default:
		return "" // O or unset: no gender filter
	}
}

func (r *Recommender) Recommend(ctx context.Context, limit int) (result []cache.Score, err error) {
	// Lifecycle-aware pool blending (Phase 2)
	if r.lifecycleProfile != nil && len(r.config.RecallPools) > 0 {
		result, err = r.recommendWithPools(ctx, limit)
		if err != nil {
			return nil, errors.Trace(err)
		}
		// Apply post-filtering: ensure correct gender in final results
		result = r.filterResultByGender(ctx, result)
		// Fallback to ranker if pool blending returns insufficient results
		if len(result) < limit {
			fbResult, _, fbErr := r.RecommendSequential(ctx, result, limit, r.config.Fallback.Recommenders...)
			if fbErr != nil {
				return result, errors.Trace(fbErr)
			}
			fbResult = r.filterResultByGender(ctx, fbResult)
			// Merge pool results with fallback (prefer pool results for correct gender)
			result = r.mergeWithFallback(ctx, result, fbResult, limit)
		}
		// Phase 4: Apply supply/demand balance boost to re-rank results.
		// Low-exposure items receive a boost multiplier to balance gender group exposure.
		result, err = r.applySupplyDemandBoost(ctx, result)
		if err != nil {
			log.Logger().Warn("failed to apply supply/demand boost",
				zap.String("user_id", r.userId),
				zap.Error(err))
			// Non-fatal: continue without supply/demand boost
		}
		// Phase 2: Apply item quality filtering.
		// Filter out items with low like rate or high block rate based on computed quality stats.
		if r.itemStatsTracker != nil {
			result = r.itemStatsTracker.FilterByQuality(result)
		}
		// Phase 3: Apply MMR diversity reranking.
		// MMR balances relevance with diversity to prevent homogeneous recommendations.
		if r.mmrReranker != nil && len(result) > 1 {
			result, _ = r.mmrReranker.Rerank(ctx, result)
		}
		// Phase 3: Apply success-rate reranking.
		// Boost items with high historical like/match rates.
		if r.successRateReranker != nil && len(result) > 1 {
			result, _ = r.successRateReranker.Rerank(ctx, result)
		}
		// Phase 3: Record recs shown for session fatigue tracking.
		if r.sessionFatigueTracker != nil && len(result) > 0 {
			_ = r.sessionFatigueTracker.RecordRecShown(ctx, r.userId, len(result))
		}
		return result, nil
	}
	if !strings.EqualFold(r.config.Ranker.Type, "none") {
		scores, err := r.cacheClient.SearchScores(ctx, cache.Recommend, r.userId, r.categories, 0, r.config.CacheSize)
		if err != nil {
			return nil, errors.Trace(err)
		}
		result = make([]cache.Score, 0, len(scores))
		for _, score := range scores {
			if !r.excludeSet.Contains(score.Id) {
				r.excludeSet.Add(score.Id)
				result = append(result, score)
			}
		}
	} else {
		result, _, err = r.RecommendSequential(ctx, result, r.config.CacheSize, r.config.Ranker.Recommenders...)
		if err != nil {
			return nil, errors.Trace(err)
		}
	}
	if len(result) >= limit && limit > 0 {
		return result[:limit], nil
	}
	result, _, err = r.RecommendSequential(ctx, result, limit, r.config.Fallback.Recommenders...)
	return result, errors.Trace(err)
}

// recommendWithPools blends recommendations from multiple lifecycle pools using soft weights.
// Each pool contributes recommenders, and the final score is weighted by the pool's weight.
// Items appearing in multiple pools accumulate weights from all pools.
// Gender cross-filtering: users are recommended to opposite-gender users only (M→F, F→M).
// When correct-gender candidates are insufficient, wrong-gender items receive heavy score penalty.
func (r *Recommender) recommendWithPools(ctx context.Context, limit int) ([]cache.Score, error) {
	// Get pool blends for this user's lifecycle profile
	poolBlends := r.lifecycleClassifier.GetPoolsForProfile(r.lifecycleProfile)
	log.Logger().Warn("pool blends for user",
		zap.String("user_id", r.userId),
		zap.Int("pool_count", len(poolBlends)))
	for _, blend := range poolBlends {
		log.Logger().Warn("pool blend",
			zap.String("pool", blend.PoolName),
			zap.Float64("weight", blend.Weight),
			zap.Strings("recommenders", blend.Recommenders))
	}
	if len(poolBlends) == 0 {
		// Fallback to sequential if no pools match
		scores, _, err := r.RecommendSequential(ctx, nil, limit, r.config.Ranker.Recommenders...)
		return scores, err
	}

	// Determine target gender for cross-filtering (M→F, F→M, O→no filter)
	targetGender := r.getTargetGender(ctx)
	canFilterByGender := targetGender != "" && targetGender != "O"

	// Phase 1: Collect all candidate itemIds from all pools (no gender info yet)
	type candidate struct {
		score     float64
		itemId   string
		timestamp time.Time
	}
	candidates := make(map[string]*candidate)

	// Phase 3: Fatigue state boosting.
	// When user is actively swiping without matches, increase fatigue pool weight
	// to inject diverse, non-personalized content and re-engage the user.
	fatigueBoost := 1.0
	if r.lifecycleProfile != nil && r.lifecycleProfile.FatigueState != nil && r.lifecycleProfile.FatigueState.SwipeCount > 0 {
		swipes := r.lifecycleProfile.FatigueState.SwipeCount
		// Progressive boost: 1 + (swipes / triggerThreshold), capped at 3×
		triggerThreshold := float64(r.config.Fatigue.TriggerSwipes)
		if triggerThreshold <= 0 {
			triggerThreshold = 50
		}
		fatigueBoost = 1.0 + float64(swipes)/triggerThreshold
		if fatigueBoost > 3.0 {
			fatigueBoost = 3.0
		}
		log.Logger().Warn("fatigue pool boost active",
			zap.String("user_id", r.userId),
			zap.Int("swipe_count", swipes),
			zap.Float64("boost_factor", fatigueBoost))
	}

	for _, blend := range poolBlends {
		poolWeight := blend.Weight
		// Boost fatigue pool when user is in active fatigue state
		if blend.PoolName == "fatigue" && fatigueBoost > 1.0 {
			poolWeight *= fatigueBoost
		}
		if poolWeight <= 0 {
			continue
		}
		for _, name := range blend.Recommenders {
			recommenderFunc, err := r.parse(name)
			if err != nil {
				log.Logger().Warn("failed to parse recommender in pool",
					zap.String("pool", blend.PoolName),
					zap.String("recommender", name),
					zap.Error(err))
				continue
			}
			scores, _, err := recommenderFunc(ctx)
			if err != nil {
				log.Logger().Warn("failed to get scores from recommender",
					zap.String("pool", blend.PoolName),
					zap.String("recommender", name),
					zap.Error(err))
				continue
			}
			if len(scores) > 0 {
				log.Logger().Warn("recommender output",
					zap.String("pool", blend.PoolName),
					zap.String("recommender", name),
					zap.Float64("pool_weight", poolWeight),
					zap.Int("score_count", len(scores)),
					zap.Float64("first_score", scores[0].Score),
					zap.String("first_id", scores[0].Id))
				for _, s := range scores {
					if r.excludeSet.Contains(s.Id) {
						continue
					}
					if existing, ok := candidates[s.Id]; ok {
						existing.score += poolWeight * s.Score
					} else {
						candidates[s.Id] = &candidate{
							score:     poolWeight * s.Score,
							itemId:   s.Id,
							timestamp: s.Timestamp,
						}
					}
				}
			} else {
				log.Logger().Warn("recommender returned no scores",
					zap.String("pool", blend.PoolName),
					zap.String("recommender", name))
			}
		}
	}

	// Phase 1.5 (Phase 2/3): Explore/exploit injection.
	// Collect fresh explore candidates from latest recommender.
	// These are items not yet seen by the user, injected based on ExploreRatio.
	var exploreCandidates []cache.Score
	exploreRatio := 0.0
	if r.lifecycleProfile != nil {
		exploreRatio = r.lifecycleProfile.ExploreRatio
	}
	// Phase 3: Session fatigue overrides explore ratio when user is fatigued in session.
	// Session fatigue = consecutive swipes without match within current session.
	// A fatigued user gets more fresh/diverse content to re-engage.
	if r.sessionFatigueTracker != nil {
		fatigued, diversityBoost := r.sessionFatigueTracker.IsFatigued(ctx, r.userId)
		if fatigued && diversityBoost > 0 {
			exploreRatio = min(0.5, exploreRatio+diversityBoost)
			log.Logger().Info("session fatigue: boosting explore ratio",
				zap.String("user_id", r.userId),
				zap.Float64("explore_ratio", exploreRatio),
				zap.Float64("diversity_boost", diversityBoost))
		}
	}
	if exploreRatio > 0 {
		// Use the behavior tracker's computed explore ratio if available
		if r.behaviorTracker != nil {
			effectiveRatio := r.behaviorTracker.GetExploreRatio(ctx, r.userId)
			if effectiveRatio > exploreRatio {
				exploreRatio = effectiveRatio
			}
		}
		// Get fresh items from latest recommender (not yet in exclude set)
		latestItems, _, _ := r.recommendLatest(ctx)
		inPrimary := make(map[string]bool)
		for id := range candidates {
			inPrimary[id] = true
		}
		for _, item := range latestItems {
			if !inPrimary[item.Id] && !r.excludeSet.Contains(item.Id) {
				exploreCandidates = append(exploreCandidates, item)
				if len(exploreCandidates) >= limit*2 {
					break
				}
			}
		}
		if len(exploreCandidates) > 0 {
			// Shuffle for randomness
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			rng.Shuffle(len(exploreCandidates), func(i, j int) {
				exploreCandidates[i], exploreCandidates[j] = exploreCandidates[j], exploreCandidates[i]
			})
			log.Logger().Info("explore candidates collected",
				zap.String("user_id", r.userId),
				zap.Float64("explore_ratio", exploreRatio),
				zap.Int("explore_count", len(exploreCandidates)))
		}
	}

	// Phase 2: Batch query items to get gender for cross-filtering
	itemIds := make([]string, 0, len(candidates))
	for id := range candidates {
		itemIds = append(itemIds, id)
	}
	genderMap := make(map[string]string) // itemId → gender
	if canFilterByGender && len(itemIds) > 0 {
		items, err := r.dataClient.BatchGetItems(ctx, itemIds, data.GetOptions{SkipHidden: true})
		if err != nil {
			log.Logger().Warn("failed to batch get items for gender filter",
				zap.String("user_id", r.userId),
				zap.Error(err))
		} else {
			for _, it := range items {
				if len(it.Categories) > 0 {
					genderMap[it.ItemId] = it.Categories[0]
				}
			}
			log.Logger().Warn("gender map populated",
				zap.String("user_id", r.userId),
				zap.String("target_gender", targetGender),
				zap.Int("items_loaded", len(items)),
				zap.Int("gender_map_size", len(genderMap)),
				zap.Int("total_candidates", len(candidates)))
		}
	} else {
		log.Logger().Warn("gender filter skipped",
			zap.String("user_id", r.userId),
			zap.String("target_gender", targetGender),
			zap.Bool("can_filter", canFilterByGender),
			zap.Int("item_ids_count", len(itemIds)))
	}

	// Phase 3: Apply gender penalties and collect sorted results
	correctGenderCount := 0
	wrongGenderCount := 0
	unknownGenderCount := 0
	topScores := make([]cache.Score, 0, 10)
	for _, c := range candidates {
		if canFilterByGender {
			g := genderMap[c.itemId]
			if g == "" {
				// Unknown gender: apply heavy penalty
				c.score *= 0.001
				unknownGenderCount++
			} else if g == targetGender {
				// Correct gender: full weight
				correctGenderCount++
			} else {
				// Wrong gender: apply heavy penalty
				c.score *= 0.001
				wrongGenderCount++
			}
		}
		if c.score > 0 && len(topScores) < 10 {
			topScores = append(topScores, cache.Score{Id: c.itemId, Score: c.score})
		}
	}

	log.Logger().Warn("recommendWithPools score detail",
		zap.String("user_id", r.userId),
		zap.String("target_gender", targetGender),
		zap.Int("correct_gender", correctGenderCount),
		zap.Int("wrong_gender", wrongGenderCount),
		zap.Int("top_scores_count", len(topScores)),
		zap.Any("top_scores", topScores))

	// Sort by weighted score descending
	sorted := make([]cache.Score, 0, len(candidates))
	for _, c := range candidates {
		if c.score > 0 {
			sorted = append(sorted, cache.Score{
				Id:        c.itemId,
				Score:     c.score,
				Timestamp: c.timestamp,
			})
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})

	log.Logger().Warn("recommendWithPools sorted top5",
		zap.String("user_id", r.userId),
		zap.String("target_gender", targetGender))
	for i := 0; i < len(sorted) && i < 5; i++ {
		g := genderMap[sorted[i].Id]
		log.Logger().Warn("recommendWithPools sorted item",
			zap.Int("rank", i),
			zap.String("item_id", sorted[i].Id),
			zap.Float64("score", sorted[i].Score),
			zap.String("item_gender", g))
	}

	// Trim to limit, then interleave explore candidates
	result := make([]cache.Score, 0, limit)
	for _, s := range sorted {
		if r.excludeSet.Contains(s.Id) {
			continue
		}
		r.excludeSet.Add(s.Id)
		result = append(result, s)
		if limit > 0 && len(result) >= limit {
			break
		}
	}

	// Interleave explore candidates based on explore_ratio
	if len(exploreCandidates) > 0 && exploreRatio > 0 && limit > 0 {
		exploreCount := int(float64(limit) * exploreRatio)
		if exploreCount > len(exploreCandidates) {
			exploreCount = len(exploreCandidates)
		}
		if exploreCount > 0 {
			// Build a set of items already in result
			inResult := make(map[string]bool)
			for _, s := range result {
				inResult[s.Id] = true
			}
			// Collect unique explore candidates not already in result
			var uniqueExplore []cache.Score
			for _, ec := range exploreCandidates {
				if !inResult[ec.Id] {
					uniqueExplore = append(uniqueExplore, ec)
					inResult[ec.Id] = true
					if len(uniqueExplore) >= exploreCount {
						break
					}
				}
			}
			// Interleave: insert explore item every (limit/exploreCount) positions
			if len(uniqueExplore) > 0 {
				step := len(result) / (len(uniqueExplore) + 1)
				if step < 1 {
					step = 1
				}
				interleaved := make([]cache.Score, 0, len(result)+len(uniqueExplore))
				expIdx := 0
				for i, s := range result {
					interleaved = append(interleaved, s)
					// Insert explore item every `step` positions
					if expIdx < len(uniqueExplore) && (i+1)%step == 0 && len(interleaved) < limit+len(uniqueExplore) {
						interleaved = append(interleaved, uniqueExplore[expIdx])
						expIdx++
					}
				}
				// Append remaining explore items at the end
				for expIdx < len(uniqueExplore) && len(interleaved) < limit+len(uniqueExplore) {
					interleaved = append(interleaved, uniqueExplore[expIdx])
					expIdx++
				}
				// Trim to original limit
				if len(interleaved) > limit {
					interleaved = interleaved[:limit]
				}
				log.Logger().Info("explore items interleaved",
					zap.String("user_id", r.userId),
					zap.Int("primary_count", len(result)),
					zap.Int("explore_count", len(uniqueExplore)),
					zap.Float64("explore_ratio", exploreRatio))
				result = interleaved
			}
		}
	}

	log.Logger().Warn("recommendWithPools result",
		zap.String("user_id", r.userId),
		zap.String("target_gender", targetGender),
		zap.Bool("can_filter", canFilterByGender),
		zap.Int("correct_gender", correctGenderCount),
		zap.Int("wrong_gender", wrongGenderCount),
		zap.Int("unknown_gender", unknownGenderCount),
		zap.Int("total_candidates", len(candidates)),
		zap.Int("result_size", len(result)))

	return result, nil
}

// RecommendSequential recommend items from multiple recommenders sequentially util reaching the limit.
// If limit <= 0, all recommendations are returned.
func (r *Recommender) RecommendSequential(ctx context.Context, result []cache.Score, limit int, names ...string) ([]cache.Score, string, error) {
	var digests []string
	for _, name := range names {
		recommenderFunc, err := r.parse(name)
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		scores, digest, err := recommenderFunc(ctx)
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		for _, score := range scores {
			r.excludeSet.Add(score.Id)
		}
		result = append(result, scores...)
		digests = append(digests, digest)
		if limit > 0 && len(result) >= limit {
			return result[:limit], util.MD5(digests...), nil
		}
	}
	return result, util.MD5(digests...), nil
}

func (r *Recommender) parse(fullname string) (RecommenderFunc, error) {
	if fullname == CollaborativeRecommender {
		return r.recommendCollaborative, nil
	} else if fullname == LatestRecommender {
		return r.recommendLatest, nil
	} else if fullname == FatigueBreakerRecommender {
		return r.recommendFatigueBreaker, nil
	} else if fullname == SocialGraphRecommender {
		return r.recommendSocialGraph, nil
	} else if fullname == ProfileMatchRecommender {
		return r.recommendProfileMatch, nil
	} else if fullname == VIPQualityPoolRecommender {
		return r.recommendVIPQualityPool, nil
	} else if fullname == InterestBasedRecommender {
		return r.recommendInterestBased, nil
	} else if fullname == TemporalRecommender {
		return r.recommendTemporal, nil
	} else if after, ok := strings.CutPrefix(fullname, NonPersonalizedRecommender); ok {
		name := after
		return r.recommendNonPersonalized(name), nil
	} else if after, ok := strings.CutPrefix(fullname, ItemToItemRecommender); ok {
		name := after
		return r.recommendItemToItem(name), nil
	} else if after, ok := strings.CutPrefix(fullname, UserToUserRecommender); ok {
		name := after
		return r.recommendUserToUser(name), nil
	} else if after, ok := strings.CutPrefix(fullname, ExternalRecommender); ok {
		name := after
		return r.recommendExternal(name), nil
	} else {
		return nil, errors.Errorf("unknown recommender: %s", fullname)
	}
}

func (r *Recommender) recommendLatest(ctx context.Context) ([]cache.Score, string, error) {
	var after *time.Time
	if r.config.DataSource.ItemTTL > 0 {
		after = new(time.Now().AddDate(0, 0, -int(r.config.DataSource.ItemTTL)))
	}
	items, err := r.dataClient.GetLatestItems(ctx, r.config.CacheSize, r.categories, after)
	if err != nil {
		return nil, "", errors.Trace(err)
	}
	scores := make([]cache.Score, 0, len(items))
	for _, item := range items {
		if !r.excludeSet.Contains(item.ItemId) {
			scores = append(scores, cache.Score{
				Id:         item.ItemId,
				Score:      float64(item.Timestamp.Unix()),
				Categories: item.Categories,
			})
		}
	}
	return scores, "latest", nil
}

// recommendFatigueBreaker returns diverse, non-personalized items to break recommendation fatigue.
// It combines latest items with random perturbation to escape the personalized echo chamber.
// When a user is fatigued (many swipes without match), their recommendations become stale;
// this recommender injects freshness and randomness to re-engage the user.
func (r *Recommender) recommendFatigueBreaker(ctx context.Context) ([]cache.Score, string, error) {
	// Get latest items for freshness
	var after *time.Time
	if r.config.DataSource.ItemTTL > 0 {
		after = new(time.Now().AddDate(0, 0, -int(r.config.DataSource.ItemTTL)))
	}
	items, err := r.dataClient.GetLatestItems(ctx, r.config.CacheSize, nil, after)
	if err != nil {
		return nil, "", errors.Trace(err)
	}

	// Apply random perturbation to scores to break echo chamber.
	// Items get a base freshness score plus a random component so the
	// same items don't appear at the top on every request.
	nowUnix := float64(time.Now().Unix())
	scores := make([]cache.Score, 0, len(items))
	for _, item := range items {
		if r.excludeSet.Contains(item.ItemId) {
			continue
		}
		// Freshness score (newer items score higher) + random perturbation
		freshness := nowUnix - float64(item.Timestamp.Unix())
		perturbation := (rand.Float64() - 0.5) * r.config.Fatigue.RandomRatio * freshness
		score := float64(item.Timestamp.Unix()) + perturbation
		scores = append(scores, cache.Score{
			Id:         item.ItemId,
			Score:      score,
			Timestamp:  item.Timestamp,
			Categories: item.Categories,
		})
	}
	return scores, FatigueBreakerRecommender, nil
}

func (r *Recommender) recommendNonPersonalized(name string) RecommenderFunc {
	return func(ctx context.Context) ([]cache.Score, string, error) {
		var categories []string
		if len(r.categories) == 0 {
			categories = []string{""}
		} else {
			categories = r.categories
		}
		// fetch items from cache
		items, err := r.cacheClient.SearchScores(ctx, cache.NonPersonalized, name, categories, 0, r.config.CacheSize)
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		// read digest
		digest, err := r.cacheClient.Get(ctx, cache.Key(cache.NonPersonalizedDigest, name)).String()
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		// remove excluded items
		return lo.Filter(items, func(item cache.Score, index int) bool {
			return !r.excludeSet.Contains(item.Id)
		}), digest, nil
	}
}

func (r *Recommender) recommendCollaborative(ctx context.Context) ([]cache.Score, string, error) {
	// fetch items from cache
	items, err := r.cacheClient.SearchScores(ctx, cache.CollaborativeFiltering, r.userId, r.categories, 0, r.config.CacheSize)
	if err != nil {
		return nil, "", errors.Trace(err)
	}
	// read digest
	digest, err := r.cacheClient.Get(ctx, cache.Key(cache.CollaborativeFilteringDigest, r.userId)).String()
	if err != nil {
		return nil, "", errors.Trace(err)
	}
	// remove excluded items
	return lo.Filter(items, func(item cache.Score, index int) bool {
		return !r.excludeSet.Contains(item.Id)
	}), digest, nil
}

func (r *Recommender) recommendItemToItem(name string) RecommenderFunc {
	return func(ctx context.Context) ([]cache.Score, string, error) {
		// filter positive feedbacks
		data.SortFeedbacks(r.userFeedback)
		userFeedback := make([]data.Feedback, 0, r.config.CacheSize)
		for _, feedback := range r.userFeedback {
			if expression.MatchFeedbackTypeExpressions(r.config.DataSource.PositiveFeedbackTypes, feedback.FeedbackType, feedback.Value) {
				userFeedback = append(userFeedback, feedback)
				if r.online && r.config.ContextSize <= len(userFeedback) {
					break
				}
			}
		}
		// collect scores
		scores := make(map[string]float64)
		categories := make(map[string][]string)
		digests := mapset.NewSet[string]()
		for _, feedback := range userFeedback {
			similarItems, err := r.cacheClient.SearchScores(ctx, cache.ItemToItem, cache.Key(name, feedback.ItemId), r.categories, 0, r.config.CacheSize)
			if err != nil {
				return nil, "", errors.Trace(err)
			}
			digest, err := r.cacheClient.Get(ctx, cache.Key(cache.ItemToItemDigest, name, feedback.ItemId)).String()
			if err != nil {
				return nil, "", errors.Trace(err)
			}
			for _, item := range similarItems {
				if !r.excludeSet.Contains(item.Id) {
					scores[item.Id] += item.Score
					categories[item.Id] = item.Categories
					digests.Add(digest)
				}
			}
		}
		// collect top scores
		filter := heap.NewTopKFilter[string, float64](r.config.CacheSize)
		for id, score := range scores {
			filter.Push(id, score)
		}
		elems := filter.PopAll()
		return lo.Map(elems, func(elem heap.Elem[string, float64], _ int) cache.Score {
			return cache.Score{
				Id:         elem.Value,
				Score:      elem.Weight,
				Categories: categories[elem.Value],
			}
		}), strings.Join(digests.ToSlice(), ""), nil
	}
}

func (r *Recommender) recommendUserToUser(name string) RecommenderFunc {
	return func(ctx context.Context) ([]cache.Score, string, error) {
		scores := make(map[string]float64)
		// load similar users
		similarUsers, err := r.cacheClient.SearchScores(ctx, cache.UserToUser, cache.Key(name, r.userId), nil, 0, r.config.CacheSize)
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		// read digest
		digest, err := r.cacheClient.Get(ctx, cache.Key(cache.UserToUserDigest, name, r.userId)).String()
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		// aggregate scores
		for _, user := range similarUsers {
			// load historical feedback
			feedbacks, err := r.dataClient.GetUserFeedback(ctx, user.Id, new(time.Now()), r.config.DataSource.PositiveFeedbackTypes...)
			if err != nil {
				return nil, "", errors.Trace(err)
			}
			// add unseen items
			for _, feedback := range feedbacks {
				if !r.excludeSet.Contains(feedback.ItemId) {
					scores[feedback.ItemId] += user.Score
				}
			}
		}
		// collect top k
		filter := heap.NewTopKFilter[string, float64](r.config.CacheSize)
		for id, score := range scores {
			filter.Push(id, score)
		}
		elems := filter.PopAll()
		// filter by categories
		results := make([]cache.Score, 0, len(elems))
		ids := lo.Map(elems, func(elem heap.Elem[string, float64], _ int) string {
			return elem.Value
		})
		var after *time.Time
		if r.config.DataSource.ItemTTL > 0 {
			after = new(time.Now().AddDate(0, 0, -int(r.config.DataSource.ItemTTL)))
		}
		items, err := r.dataClient.BatchGetItems(ctx, ids, data.GetOptions{
			SkipHidden: true,
			After:      after,
		})
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		itemsMap := make(map[string]data.Item)
		for _, item := range items {
			itemsMap[item.ItemId] = item
		}
		for _, elem := range elems {
			if item, ok := itemsMap[elem.Value]; ok && lo.Every(item.Categories, r.categories) {
				results = append(results, cache.Score{
					Id:         item.ItemId,
					Score:      elem.Weight,
					Categories: item.Categories,
				})
			}
		}
		return results, digest, nil
	}
}

func (r *Recommender) recommendExternal(name string) RecommenderFunc {
	return func(ctx context.Context) ([]cache.Score, string, error) {
		var externalConfig config.ExternalConfig
		for _, extConfig := range r.config.External {
			if extConfig.Name == name {
				externalConfig = extConfig
				break
			}
		}

		if len(r.categories) > 0 {
			// external recommenders do not support categories
			return nil, externalConfig.Hash(), nil
		}

		external, err := NewExternal(externalConfig)
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		defer external.Close()
		items, err := external.Pull(r.userId)
		if err != nil {
			return nil, "", errors.Trace(err)
		}
		scores := make([]cache.Score, 0, len(items))
		for _, itemId := range items {
			if !r.excludeSet.Contains(itemId) {
				scores = append(scores, cache.Score{
					Id: itemId,
				})
			}
		}
		return scores, externalConfig.Hash(), nil
	}
}

// filterResultByGender applies gender cross-filtering to recommendation results.
// For dating apps: M→F, F→M, O→no filter. Returns items of correct gender only.
func (r *Recommender) filterResultByGender(ctx context.Context, results []cache.Score) []cache.Score {
	if len(results) == 0 {
		return results
	}
	targetGender := r.getTargetGender(ctx)
	if targetGender == "" || targetGender == "O" {
		// No gender filter needed
		return results
	}

	// Batch fetch items to get their gender
	itemIds := make([]string, len(results))
	for i, s := range results {
		itemIds[i] = s.Id
	}
	items, err := r.dataClient.BatchGetItems(ctx, itemIds, data.GetOptions{SkipHidden: true})
	if err != nil {
		log.Logger().Warn("filterResultByGender failed to batch get items",
			zap.String("user_id", r.userId),
			zap.Error(err))
		return results // Return unfiltered on error
	}
	itemGenderMap := make(map[string]string, len(items))
	for _, it := range items {
		if len(it.Categories) > 0 {
			itemGenderMap[it.ItemId] = it.Categories[0]
		}
	}

	// Filter to correct gender only
	filtered := make([]cache.Score, 0, len(results))
	wrongCount := 0
	for _, s := range results {
		g := itemGenderMap[s.Id]
		if g == targetGender {
			filtered = append(filtered, s)
		} else {
			wrongCount++
		}
	}
	log.Logger().Warn("filterResultByGender",
		zap.String("user_id", r.userId),
		zap.String("target_gender", targetGender),
		zap.Int("input_count", len(results)),
		zap.Int("output_count", len(filtered)),
		zap.Int("wrong_gender_filtered", wrongCount))
	return filtered
}

// mergeWithFallback combines pool results with fallback results.
// Priority: pool results first (correct gender from pools), then fill remaining slots
// from fallback results (also gender-filtered).
func (r *Recommender) mergeWithFallback(ctx context.Context, poolResult, fbResult []cache.Score, limit int) []cache.Score {
	if len(poolResult) >= limit {
		return poolResult[:limit]
	}

	// Deduplicate fallback results (avoid showing items already in poolResult)
	seen := mapset.NewSet[string]()
	for _, s := range poolResult {
		seen.Add(s.Id)
	}
	uniqueFb := make([]cache.Score, 0, len(fbResult))
	for _, s := range fbResult {
		if !seen.Contains(s.Id) {
			seen.Add(s.Id)
			uniqueFb = append(uniqueFb, s)
		}
	}

	// Append unique fallback results
	result := append(poolResult, uniqueFb...)
	if len(result) > limit {
		result = result[:limit]
	}
	log.Logger().Warn("mergeWithFallback",
		zap.String("user_id", r.userId),
		zap.Int("pool_count", len(poolResult)),
		zap.Int("fb_count", len(fbResult)),
		zap.Int("unique_fb_count", len(uniqueFb)),
		zap.Int("final_count", len(result)))
	return result
}

// SupplyDemandTracker implements gender-group exposure balancing for recommendations.
// It tracks how many times items of each gender group have been recommended today,
// then boosts low-exposure items to re-balance supply across gender groups.
type SupplyDemandTracker struct {
	cfg   config.SupplyDemandConfig
	redis *redis.Client
	data  data.Database
}

// NewSupplyDemandTracker creates a supply/demand tracker.
func NewSupplyDemandTracker(
	cfg config.SupplyDemandConfig,
	redisClient *redis.Client,
	dataClient data.Database,
) *SupplyDemandTracker {
	return &SupplyDemandTracker{
		cfg:   cfg,
		redis: redisClient,
		data:  dataClient,
	}
}

// applySupplyDemandBoost re-ranks recommendations by applying a boost to
// low-exposure items. Items whose gender group is below the exposure threshold
// receive a boost multiplier (up to cfg.LowExposureBoost), making them more
// likely to appear higher in the final results. After boosting, items are
// re-sorted by boosted score and their exposure is recorded in Redis.
func (t *SupplyDemandTracker) ApplyBoost(ctx context.Context, results []cache.Score) ([]cache.Score, error) {
	if len(results) == 0 || !t.cfg.Enabled {
		return results, nil
	}

	// Build today's Redis key for each gender group
	today := time.Now().UTC().Format("2006-01-02")
	itemIds := make([]string, len(results))
	for i, s := range results {
		itemIds[i] = s.Id
	}

	// Batch-fetch items to get their gender
	items, err := t.data.BatchGetItems(ctx, itemIds, data.GetOptions{SkipHidden: true})
	if err != nil {
		return nil, errors.Trace(err)
	}
	itemGenderMap := make(map[string]string, len(items))
	for _, it := range items {
		if len(it.Categories) > 0 {
			itemGenderMap[it.ItemId] = it.Categories[0]
		}
	}

	// Batch-fetch current exposure counts per gender group from Redis
	// Redis key: sd:exposure:{gender}:{date}
	exposureMap := make(map[string]int) // itemId → current daily exposure count
	genderGroups := t.cfg.GenderGroups
	if len(genderGroups) == 0 {
		genderGroups = []string{"M", "F", "O"}
	}

	if t.redis != nil {
		pipe := t.redis.Pipeline()
		pipeResults := make([]*redis.MapStringStringCmd, len(genderGroups))
		for i, gender := range genderGroups {
			key := fmt.Sprintf("sd:exposure:%s:%s", gender, today)
			pipeResults[i] = pipe.HGetAll(ctx, key)
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			log.Logger().Warn("failed to fetch supply/demand exposure",
				zap.Error(err))
		}
		for i := range genderGroups {
			val, err := pipeResults[i].Result()
			if err == nil {
				for itemId, expStr := range val {
					var exp int
					fmt.Sscanf(expStr, "%d", &exp)
					exposureMap[itemId] = exp
				}
			}
		}
	}

	// Apply boost: items below threshold get boosted
	threshold := float64(t.cfg.LowExposureThreshold)
	if threshold <= 0 {
		threshold = 10
	}
	boost := t.cfg.LowExposureBoost
	if boost <= 0 {
		boost = 1.5
	}

	boosted := make([]cache.Score, 0, len(results))
	exposureUpdates := make(map[string]int) // itemId → increment by 1
	boostStats := make(map[string]int)     // gender → count of boosted items

	for _, s := range results {
		gender := itemGenderMap[s.Id]
		if gender == "" {
			gender = "O" // default to O for unknown gender
		}
		exp := exposureMap[s.Id]
		var finalScore float64
		if exp == 0 {
			// Never exposed: maximum boost
			finalScore = s.Score * boost
			boostStats[gender]++
		} else if float64(exp) < threshold {
			// Partial boost proportional to how far below threshold
			fraction := 1.0 - float64(exp)/threshold
			multiplier := 1.0 + fraction*(boost-1.0)
			finalScore = s.Score * multiplier
			boostStats[gender]++
		} else {
			// Above threshold: no boost
			finalScore = s.Score
		}
		boosted = append(boosted, cache.Score{
			Id:        s.Id,
			Score:     finalScore,
			Timestamp: s.Timestamp,
		})
		exposureUpdates[s.Id] = 1
	}

	// Sort by boosted score descending
	sort.Slice(boosted, func(i, j int) bool {
		return boosted[i].Score > boosted[j].Score
	})

	// Record exposure asynchronously (increment count for each recommended item)
	if t.redis != nil && len(exposureUpdates) > 0 {
		go t.recordExposure(context.Background(), itemGenderMap, exposureUpdates, today)
	}

	log.Logger().Warn("supply/demand boost applied",
		zap.Int("result_count", len(results)),
		zap.Int("threshold", int(threshold)),
		zap.Float64("boost", boost),
		zap.Any("boost_stats", boostStats))

	return boosted, nil
}

// recordExposure increments the daily exposure counter for recommended items in Redis.
// This runs in a goroutine and does not block the recommendation response.
func (t *SupplyDemandTracker) recordExposure(ctx context.Context, itemGenderMap map[string]string, updates map[string]int, date string) {
	if t.redis == nil {
		return
	}
	// Group updates by gender
	genderUpdates := make(map[string][]string)
	for itemId := range updates {
		gender := itemGenderMap[itemId]
		if gender == "" {
			gender = "O"
		}
		genderUpdates[gender] = append(genderUpdates[gender], itemId)
	}
	// Batch update each gender group's exposure hash
	pipe := t.redis.Pipeline()
	for gender, itemIds := range genderUpdates {
		key := fmt.Sprintf("sd:exposure:%s:%s", gender, date)
		for _, itemId := range itemIds {
			pipe.HIncrBy(ctx, key, itemId, 1)
		}
		pipe.Expire(ctx, key, 25*time.Hour) // TTL slightly over 24h to cover timezone overlap
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Logger().Warn("failed to record supply/demand exposure", zap.Error(err))
	}
}

// applySupplyDemandBoost is the Recommender wrapper that delegates to SupplyDemandTracker.
func (r *Recommender) applySupplyDemandBoost(ctx context.Context, results []cache.Score) ([]cache.Score, error) {
	if r.supplyDemandTracker == nil {
		return results, nil
	}
	return r.supplyDemandTracker.ApplyBoost(ctx, results)
}

