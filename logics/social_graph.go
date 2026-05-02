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

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/gorse-io/gorse/common/expression"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

// SocialGraphConfig controls social-graph-based recommendation behavior.
type SocialGraphConfig struct {
	// MatchThreshold switches from Match-Liked-Overlap to FoF when user has >= this many matches.
	// Default: 10. Set to 0 to always use Match-Liked-Overlap.
	MatchThreshold int `mapstructure:"match_threshold"`
	// MaxFriends caps how many matched users to fetch (most-recent first).
	MaxFriends int `mapstructure:"max_friends"`
	// FoFMaxHops limits how many hops in the FoF traversal (2 = 2-hop FoF).
	FoFMaxHops int `mapstructure:"fof_max_hops"`
	// FoFMaxFriendsPerNode caps matched users fetched per 1-hop friend in FoF mode.
	FoFMaxFriendsPerNode int `mapstructure:"fof_max_friends_per_node"`
	// MatchFeedbackType is the feedback type used to identify matches (mutual likes).
	MatchFeedbackType string `mapstructure:"match_feedback_type"`
	// PositiveFeedbackTypes are positive interactions to propagate.
	PositiveFeedbackTypes []string `mapstructure:"positive_feedback_types"`
}

func defaultSocialGraphConfig() SocialGraphConfig {
	return SocialGraphConfig{
		MatchThreshold:       10,
		MaxFriends:          50,
		FoFMaxHops:          2,
		FoFMaxFriendsPerNode: 30,
		MatchFeedbackType:   "match",
		PositiveFeedbackTypes: []string{"like"},
	}
}

// recommendSocialGraph recommends items based on the social graph.
// It uses an adaptive strategy:
//   - Sparse (matches < threshold): Match-Liked-Overlap (1-hop)
//   - Dense (matches >= threshold): Friends-of-Friends (2-hop)
//
// Match-Liked-Overlap:
//   1. Find all users the current user has matched with.
//   2. Collect items those matched users have liked.
//   3. Score items by how many matched users liked them.
//
// Friends-of-Friends (FoF):
//   1. Find all users the current user has matched with (1-hop friends).
//   2. For each 1-hop friend, find their matches (2-hop friends).
//   3. Collect items liked by 2-hop friends that the current user hasn't seen.
//   4. Score items by FoF path multiplicity.
func (r *Recommender) recommendSocialGraph(ctx context.Context) ([]cache.Score, string, error) {
	cfg := defaultSocialGraphConfig()
	if r.config.SocialGraph.MatchThreshold > 0 {
		cfg.MatchThreshold = r.config.SocialGraph.MatchThreshold
	}
	if r.config.SocialGraph.MaxFriends > 0 {
		cfg.MaxFriends = r.config.SocialGraph.MaxFriends
	}
	if r.config.SocialGraph.FoFMaxHops > 0 {
		cfg.FoFMaxHops = r.config.SocialGraph.FoFMaxHops
	}
	if r.config.SocialGraph.FoFMaxFriendsPerNode > 0 {
		cfg.FoFMaxFriendsPerNode = r.config.SocialGraph.FoFMaxFriendsPerNode
	}

	excludeSet := r.ExcludeSet()

	// Step 1: Get all matches for current user (social edges = mutual likes).
	myMatches, err := r.dataClient.GetUserFeedback(ctx, r.userId, nil,
		expression.MustParseFeedbackTypeExpression(cfg.MatchFeedbackType))
	if err != nil {
		return nil, "", err
	}

	if len(myMatches) == 0 {
		return nil, SocialGraphRecommender, nil
	}

	matchThreshold := cfg.MatchThreshold
	if matchThreshold <= 0 {
		matchThreshold = 10
	}

	if len(myMatches) >= matchThreshold && cfg.FoFMaxHops >= 2 {
		return r.socialGraphFoF(ctx, cfg, myMatches, excludeSet)
	}
	return r.socialGraphOverlap(ctx, cfg, myMatches, excludeSet)
}

// socialGraphOverlap implements the 1-hop Match-Liked-Overlap strategy.
// Collects items liked by matched users and scores by overlap count.
func (r *Recommender) socialGraphOverlap(
	ctx context.Context,
	cfg SocialGraphConfig,
	myMatches []data.Feedback,
	excludeSet mapset.Set[string],
) ([]cache.Score, string, error) {
	// Gather matched user IDs (deduplicated).
	// In match feedback, ItemId is the matched user ID.
	matchUserIds := lo.Uniq(lo.Map(myMatches, func(f data.Feedback, _ int) string {
		return f.ItemId
	}))
	if len(matchUserIds) > cfg.MaxFriends {
		matchUserIds = matchUserIds[:cfg.MaxFriends]
	}

	// itemId → how many matched users liked it.
	itemScore := make(map[string]int)

	exprs := make([]expression.FeedbackTypeExpression, len(cfg.PositiveFeedbackTypes))
	for i, ft := range cfg.PositiveFeedbackTypes {
		exprs[i] = expression.MustParseFeedbackTypeExpression(ft)
	}

	for _, uid := range matchUserIds {
		likes, err := r.dataClient.GetUserFeedback(ctx, uid, nil, exprs...)
		if err != nil {
			zap.L().Warn("failed to get likes for social graph", zap.String("user_id", uid), zap.Error(err))
			continue
		}
		for _, like := range likes {
			if excludeSet.Contains(like.ItemId) {
				continue
			}
			itemScore[like.ItemId]++
		}
	}

	if len(itemScore) == 0 {
		return nil, "", nil
	}

	results := lo.MapToSlice(itemScore, func(itemId string, count int) cache.Score {
		return cache.Score{Id: itemId, Score: float64(count)}
	})
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	zap.L().Debug("social_graph overlap",
		zap.String("user_id", r.userId),
		zap.Int("matched_users", len(matchUserIds)),
		zap.Int("candidates", len(results)))

	return results, SocialGraphRecommender, nil
}

// socialGraphFoF implements the 2-hop Friends-of-Friends strategy.
// Finds items liked by 2-hop friends and scores by path multiplicity.
func (r *Recommender) socialGraphFoF(
	ctx context.Context,
	cfg SocialGraphConfig,
	myMatches []data.Feedback,
	excludeSet mapset.Set[string],
) ([]cache.Score, string, error) {
	// 1-hop friends: users the current user matched with.
	hop1Ids := lo.Uniq(lo.Map(myMatches, func(f data.Feedback, _ int) string {
		return f.ItemId
	}))
	if len(hop1Ids) > cfg.MaxFriends {
		hop1Ids = hop1Ids[:cfg.MaxFriends]
	}

	// Build expression slices once.
	matchExpr := expression.MustParseFeedbackTypeExpression(cfg.MatchFeedbackType)
	positiveExprs := make([]expression.FeedbackTypeExpression, len(cfg.PositiveFeedbackTypes))
	for i, ft := range cfg.PositiveFeedbackTypes {
		positiveExprs[i] = expression.MustParseFeedbackTypeExpression(ft)
	}

	// itemId → FoF path count.
	itemScore := make(map[string]int)

	for _, hop1Id := range hop1Ids {
		// Get 2-hop friends (who matched with hop1).
		hop2Feedback, err := r.dataClient.GetUserFeedback(ctx, hop1Id, nil, matchExpr)
		if err != nil {
			zap.L().Warn("failed to get matches for FoF", zap.String("hop1_id", hop1Id), zap.Error(err))
			continue
		}

		hop2Ids := lo.Uniq(lo.Map(hop2Feedback, func(f data.Feedback, _ int) string {
			return f.ItemId
		}))
		if len(hop2Ids) > cfg.FoFMaxFriendsPerNode {
			hop2Ids = hop2Ids[:cfg.FoFMaxFriendsPerNode]
		}

		for _, hop2Id := range hop2Ids {
			// Skip self and already-matched users.
			if hop2Id == r.userId || lo.Contains(hop1Ids, hop2Id) {
				continue
			}

			likes, err := r.dataClient.GetUserFeedback(ctx, hop2Id, nil, positiveExprs...)
			if err != nil {
				continue
			}
			for _, like := range likes {
				if excludeSet.Contains(like.ItemId) {
					continue
				}
				// Each 2-hop path contributes 1 to the score.
				itemScore[like.ItemId]++
			}
		}
	}

	if len(itemScore) == 0 {
		return nil, "", nil
	}

	results := lo.MapToSlice(itemScore, func(itemId string, count int) cache.Score {
		return cache.Score{Id: itemId, Score: float64(count)}
	})
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	zap.L().Debug("social_graph FoF",
		zap.String("user_id", r.userId),
		zap.Int("hop1_count", len(hop1Ids)),
		zap.Int("candidates", len(results)))

	return results, SocialGraphRecommender, nil
}
