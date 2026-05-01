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

func (r *Recommender) Recommend(ctx context.Context, limit int) (result []cache.Score, err error) {
	// Lifecycle-aware pool blending (Phase 2)
	if r.lifecycleProfile != nil && len(r.config.RecallPools) > 0 {
		result, err = r.recommendWithPools(ctx, limit)
		if err != nil {
			return nil, errors.Trace(err)
		}
		// Fallback to ranker if pool blending returns insufficient results
		if len(result) < limit {
			fbResult, _, fbErr := r.RecommendSequential(ctx, result, limit, r.config.Fallback.Recommenders...)
			if fbErr != nil {
				return result, errors.Trace(fbErr)
			}
			result = fbResult
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
func (r *Recommender) recommendWithPools(ctx context.Context, limit int) ([]cache.Score, error) {
	// Get pool blends for this user's lifecycle profile
	poolBlends := r.lifecycleClassifier.GetPoolsForProfile(r.lifecycleProfile)
	if len(poolBlends) == 0 {
		// Fallback to sequential if no pools match
		scores, _, err := r.RecommendSequential(ctx, nil, limit, r.config.Ranker.Recommenders...)
		return scores, err
	}

	// Collect candidates from each pool, weighted by pool weight
	type candidate struct {
		score     float64
		itemId   string
		timestamp time.Time
	}
	candidates := make(map[string]*candidate)

	for _, blend := range poolBlends {
		poolWeight := blend.Weight
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
			for _, s := range scores {
				if r.excludeSet.Contains(s.Id) {
					continue
				}
				ws := poolWeight * s.Score
				if existing, ok := candidates[s.Id]; ok {
					existing.score += ws
				} else {
					candidates[s.Id] = &candidate{
						score:     ws,
						itemId:   s.Id,
						timestamp: s.Timestamp,
					}
				}
			}
		}
	}

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

	// Mark excluded items and trim to limit
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
