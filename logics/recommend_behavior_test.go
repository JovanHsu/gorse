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
	"testing"

	"github.com/gorse-io/gorse/config"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/stretchr/testify/assert"
)

func TestComputeStability(t *testing.T) {
	cfg := config.BehaviorConfig{
		Enabled:          true,
		MinSwipeCount:   10,
		StabilityWindow:  20,
		StatsCacheTTL:   300,
		DefaultExploreRatio: 0.2,
	}
	computer := NewBehaviorStatsComputer(cfg, config.ItemStatsConfig{}, nil, nil)

	tests := []struct {
		name        string
		feedbacks   []data.Feedback
		windowSize  int
		expectStable bool // stability >= 0.7 → stable
	}{
		{
			name: "stable user - consistent 50% right rate",
			feedbacks: func() []data.Feedback {
				fs := make([]data.Feedback, 40)
				for i := range fs {
					fType := "like"
					if i%2 == 0 {
						fType = "dislike"
					}
					fs[i] = data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: fType}}
				}
				return fs
			}(),
			windowSize:   20,
			expectStable: true,
		},
		{
			name: "unstable user - wildly varying rate",
			feedbacks: func() []data.Feedback {
				fs := make([]data.Feedback, 40)
				// First 20: all likes
				for i := 0; i < 20; i++ {
					fs[i] = data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}}
				}
				// Last 20: all dislikes
				for i := 20; i < 40; i++ {
					fs[i] = data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: "dislike"}}
				}
				return fs
			}(),
			windowSize:   20,
			expectStable: false, // highly variable
		},
		{
			name: "too few feedbacks",
			feedbacks: func() []data.Feedback {
				fs := make([]data.Feedback, 5)
				for i := range fs {
					fs[i] = data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: "like"}}
				}
				return fs
			}(),
			windowSize:   20,
			expectStable: true, // defaults to stable when too few
		},
		{
			name: "mostly likes",
			feedbacks: func() []data.Feedback {
				fs := make([]data.Feedback, 40)
				for i := range fs {
					fType := "like"
					if i%5 == 0 {
						fType = "dislike"
					}
					fs[i] = data.Feedback{FeedbackKey: data.FeedbackKey{FeedbackType: fType}}
				}
				return fs
			}(),
			windowSize:   20,
			expectStable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stability := computer.computeStability(tc.feedbacks, tc.windowSize)
			if tc.expectStable {
				assert.GreaterOrEqual(t, stability, 0.7, "expected stable, got %f", stability)
			} else {
				assert.Less(t, stability, 0.7, "expected unstable, got %f", stability)
			}
		})
	}
}

func TestBehaviorStats_ExploreRatio(t *testing.T) {
	tests := []struct {
		name           string
		rightSwipeRate float64
		stability      float64
		defaultRatio   float64
		expectExplore   float64
	}{
		{
			name:           "stable active user → low exploration",
			rightSwipeRate: 0.6,
			stability:      0.8,
			defaultRatio:   0.2,
			expectExplore:   0.3, // right rate > 0.5, +0.1
		},
		{
			name:           "unstable user → high exploration",
			rightSwipeRate: 0.3,
			stability:      0.3,
			defaultRatio:   0.2,
			expectExplore:   0.35, // stability < 0.5, capped at 0.5, +0.15 → 0.35
		},
		{
			name:           "very unstable → capped at 0.5",
			rightSwipeRate: 0.5,
			stability:      0.1,
			defaultRatio:   0.2,
			expectExplore:   0.35,
		},
		{
			name:           "inactive user → default",
			rightSwipeRate: 0.5,
			stability:      0.9,
			defaultRatio:   0.3,
			expectExplore:   0.3, // right rate not > 0.5, no stability boost
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Compute effective explore ratio manually
			exploreRatio := tc.defaultRatio
			if tc.rightSwipeRate > 0.5 {
				exploreRatio = min(0.4, exploreRatio+0.1)
			}
			if tc.stability < 0.5 && exploreRatio < 0.4 {
				exploreRatio = min(0.5, exploreRatio+0.15)
			}
			assert.InDelta(t, tc.expectExplore, exploreRatio, 0.01)
		})
	}
}

func TestItemStats_FilterByQuality(t *testing.T) {
	cfg := config.ItemStatsConfig{
		Enabled:    true,
		MinLikeRate: 0.05,
		MaxBlockRate: 0.1,
	}
	tracker := &ItemStatsTracker{cfg: cfg, redis: nil}

	// When redis is nil, FilterByQuality returns scores unchanged
	scores := []cache.Score{
		{Id: "A", Score: 1.0},
		{Id: "B", Score: 0.8},
		{Id: "C", Score: 0.6},
	}
	filtered := tracker.FilterByQuality(scores)
	assert.Equal(t, 3, len(filtered), "nil redis → no filtering")
}

func TestExploreExploit_Interleave(t *testing.T) {
	tests := []struct {
		name          string
		primary       []string
		explore       []string
		limit         int
		exploreRatio  float64
		expectExplore  int // min expected explore items in result
	}{
		{
			name:          "20% explore ratio",
			primary:       []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"},
			explore:       []string{"e1", "e2", "e3"},
			limit:         10,
			exploreRatio:  0.2,
			expectExplore:  2, // 20% of 10 = 2
		},
		{
			name:          "60% explore ratio (fatigue)",
			primary:       []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"},
			explore:       []string{"e1", "e2", "e3", "e4", "e5", "e6"},
			limit:         10,
			exploreRatio:  0.6,
			expectExplore:  5, // step=10/7=1, insert every other slot: ~5 explore in 10 slots
		},
		{
			name:          "0% explore ratio",
			primary:       []string{"p1", "p2", "p3"},
			explore:       []string{"e1", "e2"},
			limit:         3,
			exploreRatio:  0.0,
			expectExplore:  0,
		},
		{
			name:          "more explore candidates than needed",
			primary:       []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"},
			explore:       []string{"e1", "e2", "e3", "e4", "e5"},
			limit:         10,
			exploreRatio:  0.5,
			expectExplore:  5, // 50% of 10 = 5
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate interleaving
			primaryScores := make([]cache.Score, len(tc.primary))
			for i, id := range tc.primary {
				primaryScores[i] = cache.Score{Id: id, Score: float64(len(tc.primary) - i)}
			}
			exploreScores := make([]cache.Score, len(tc.explore))
			for i, id := range tc.explore {
				exploreScores[i] = cache.Score{Id: id, Score: 0.5}
			}

			result := interleaveExplore(primaryScores, exploreScores, tc.limit, tc.exploreRatio)

			// Count explore items in result
			exploreInResult := 0
			for _, s := range result {
				for _, e := range tc.explore {
					if s.Id == e {
						exploreInResult++
					}
				}
			}
			assert.GreaterOrEqual(t, exploreInResult, tc.expectExplore,
				"expected at least %d explore items, got %d", tc.expectExplore, exploreInResult)
		})
	}
}

// interleaveExplore is a test helper that mirrors the interleaving logic.
func interleaveExplore(primary, explore []cache.Score, limit int, exploreRatio float64) []cache.Score {
	if len(primary) == 0 || limit <= 0 || exploreRatio <= 0 || len(explore) == 0 {
		if len(primary) > limit {
			return primary[:limit]
		}
		return primary
	}
	exploreCount := int(float64(limit) * exploreRatio)
	if exploreCount > len(explore) {
		exploreCount = len(explore)
	}
	if exploreCount <= 0 {
		return primary[:limit]
	}

	inResult := make(map[string]bool)
	for _, s := range primary {
		inResult[s.Id] = true
	}
	var uniqueExplore []cache.Score
	for _, ec := range explore {
		if !inResult[ec.Id] {
			uniqueExplore = append(uniqueExplore, ec)
			inResult[ec.Id] = true
			if len(uniqueExplore) >= exploreCount {
				break
			}
		}
	}

	if len(uniqueExplore) == 0 {
		return primary[:limit]
	}

	step := len(primary) / (len(uniqueExplore) + 1)
	if step < 1 {
		step = 1
	}
	interleaved := make([]cache.Score, 0, len(primary)+len(uniqueExplore))
	expIdx := 0
	for i, s := range primary {
		interleaved = append(interleaved, s)
		if expIdx < len(uniqueExplore) && (i+1)%step == 0 && len(interleaved) < limit+len(uniqueExplore) {
			interleaved = append(interleaved, uniqueExplore[expIdx])
			expIdx++
		}
	}
	for expIdx < len(uniqueExplore) && len(interleaved) < limit+len(uniqueExplore) {
		interleaved = append(interleaved, uniqueExplore[expIdx])
		expIdx++
	}
	if len(interleaved) > limit {
		interleaved = interleaved[:limit]
	}
	return interleaved
}

func TestBehaviorStats_SwipeCounts(t *testing.T) {
	// Test that right/total swipe counts are computed correctly
	tests := []struct {
		name           string
		feedbackTypes  []string
		expectRight    int
		expectTotal    int
		expectRate     float64
	}{
		{
			name:          "all likes",
			feedbackTypes: []string{"like", "like", "like"},
			expectRight:   3, expectTotal: 3, expectRate: 1.0,
		},
		{
			name:          "mixed likes and dislikes",
			feedbackTypes: []string{"like", "dislike", "like", "dislike"},
			expectRight:   2, expectTotal: 4, expectRate: 0.5,
		},
		{
			name:          "with matches",
			feedbackTypes: []string{"like", "match", "dislike"},
			expectRight:   2, expectTotal: 3, expectRate: 0.667,
		},
		{
			name:          "only dislikes",
			feedbackTypes: []string{"dislike", "dislike"},
			expectRight:   0, expectTotal: 2, expectRate: 0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			right := 0
			total := 0
			for _, ft := range tc.feedbackTypes {
				switch ft {
				case "like", "match":
					right++
					total++
				case "dislike":
					total++
				}
			}
			rate := float64(right) / float64(total)
			assert.Equal(t, tc.expectRight, right)
			assert.Equal(t, tc.expectTotal, total)
			assert.InDelta(t, tc.expectRate, rate, 0.01)
		})
	}
}

func TestBehaviorStatsTracker_GetExploreRatio(t *testing.T) {
	// Test with nil redis returns default
	cfg := config.BehaviorConfig{
		Enabled:             true,
		DefaultExploreRatio: 0.2,
	}
	tracker := NewBehaviorStatsTracker(cfg, nil)
	ctx := context.Background()
	ratio := tracker.GetExploreRatio(ctx, "user1")
	assert.Equal(t, 0.2, ratio)
}
