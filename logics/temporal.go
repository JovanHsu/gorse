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
	"time"

	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
)

// TemporalRecommender is registered in recommend.go
// recommendTemporal returns items whose active_hours include the current hour,
// sorted by photo_count + quality_score within the active window.
func (r *Recommender) recommendTemporal(ctx context.Context) ([]cache.Score, string, error) {
	hour := time.Now().Hour()

	// Fetch candidates from latest items
	fetchLimit := 500
	var items []data.Item
	items, err := r.dataClient.GetLatestItems(ctx, fetchLimit, r.categories, nil)
	if err != nil {
		return nil, "", err
	}

	type scoredItem struct {
		item        data.Item
		photoCount  int
		qualityScore float64
	}

	var active []scoredItem
	for _, item := range items {
		if r.excludeSet.Contains(item.ItemId) {
			continue
		}
		if !isItemActiveAtHour(item.Labels, hour) {
			continue
		}
		pc, qs := parsePhotoCountAndQuality(item.Labels)
		active = append(active, scoredItem{
			item:        item,
			photoCount:  pc,
			qualityScore: qs,
		})
		if len(active) >= 200 {
			break
		}
	}
	if len(active) == 0 {
		return nil, TemporalRecommender, nil
	}

	// Sort by photo_count + quality_score descending
	sort.Slice(active, func(i, j int) bool {
		scoreI := float64(active[i].photoCount) + active[i].qualityScore
		scoreJ := float64(active[j].photoCount) + active[j].qualityScore
		return scoreI > scoreJ
	})

	// Trim to 50
	if len(active) > 50 {
		active = active[:50]
	}

	scores := make([]cache.Score, 0, len(active))
	for _, si := range active {
		score := float64(si.photoCount) + si.qualityScore
		scores = append(scores, cache.Score{
			Id:        si.item.ItemId,
			Score:     score,
			Timestamp: si.item.Timestamp,
		})
	}
	return scores, TemporalRecommender, nil
}

// isItemActiveAtHour checks if Labels["active_hours"] contains the given hour.
func isItemActiveAtHour(labels any, hour int) bool {
	hours := extractIntSlice(labels, "active_hours")
	if len(hours) == 0 {
		return false
	}
	for _, h := range hours {
		if h == hour {
			return true
		}
	}
	return false
}

// parsePhotoCountAndQuality extracts photo_count and quality_score from item labels.
func parsePhotoCountAndQuality(labels any) (photoCount int, qualityScore float64) {
	if labels == nil {
		return 0, 0
	}
	switch typed := labels.(type) {
	case map[string]any:
		if v, ok := typed["photo_count"].(float64); ok {
			photoCount = int(v)
		}
		if v, ok := typed["quality_score"].(float64); ok {
			qualityScore = v
		}
	case map[any]any:
		if v, ok := typed["photo_count"].(float64); ok {
			photoCount = int(v)
		}
		if v, ok := typed["quality_score"].(float64); ok {
			qualityScore = v
		}
	}
	return
}

// extractIntSlice extracts a []int slice from labels for the given key.
func extractIntSlice(labels any, key string) []int {
	if labels == nil {
		return nil
	}
	switch typed := labels.(type) {
	case map[string]any:
		v, ok := typed[key]
		if !ok {
			return nil
		}
		return anyToIntSlice(v)
	case map[any]any:
		v, ok := typed[key]
		if !ok {
			return nil
		}
		return anyToIntSlice(v)
	}
	return nil
}

// anyToIntSlice converts an any value to []int.
func anyToIntSlice(v any) []int {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []int:
		return s
	case []int64:
		out := make([]int, len(s))
		for i, n := range s {
			out[i] = int(n)
		}
		return out
	case []float64:
		out := make([]int, 0, len(s))
		for _, f := range s {
			out = append(out, int(f))
		}
		return out
	case []any:
		out := make([]int, 0, len(s))
		for _, elem := range s {
			switch n := elem.(type) {
			case float64:
				out = append(out, int(n))
			case float32:
				out = append(out, int(n))
			case int:
				out = append(out, n)
			case int64:
				out = append(out, int(n))
			}
		}
		return out
	}
	return nil
}
