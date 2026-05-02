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

package ctr

import (
	"context"

	"github.com/gorse-io/gorse/storage/data"
)

// CVRModel computes conversion rate (CVR) for items based on feedback ratios.
// CVR = chat_count / match_count for each item.
type CVRModel struct {
	dataClient data.Database
}

// NewCVRModel creates a new CVR model.
func NewCVRModel(dataClient data.Database) *CVRModel {
	return &CVRModel{dataClient: dataClient}
}

// ComputeCVRScores computes CVR scores for a list of items.
// It returns a map of itemId -> cvr_score (chat_count / match_count).
// Results are also written to each item's Labels["cvr_score"].
func (m *CVRModel) ComputeCVRScores(ctx context.Context, items []*data.Item) (map[string]float64, error) {
	if len(items) == 0 {
		return nil, nil
	}

	// Count match and chat feedback for each item
	matchCounts := make(map[string]int)
	chatCounts := make(map[string]int)

	for _, item := range items {
		// Get match feedback
		matchFbs, err := m.dataClient.GetItemFeedback(ctx, item.ItemId, "match")
		if err != nil {
			return nil, err
		}
		matchCounts[item.ItemId] = len(matchFbs)

		// Get chat feedback
		chatFbs, err := m.dataClient.GetItemFeedback(ctx, item.ItemId, "chat")
		if err != nil {
			return nil, err
		}
		chatCounts[item.ItemId] = len(chatFbs)
	}

	// Compute CVR for each item
	scores := make(map[string]float64)
	for _, item := range items {
		matchCount := matchCounts[item.ItemId]
		chatCount := chatCounts[item.ItemId]

		var cvr float64
		if matchCount > 0 {
			cvr = float64(chatCount) / float64(matchCount)
		}
		scores[item.ItemId] = cvr

		// Write CVR score back to item labels
		if item.Labels == nil {
			item.Labels = make(map[string]any)
		}
		if labels, ok := item.Labels.(map[string]any); ok {
			labels["cvr_score"] = cvr
		}
	}
	return scores, nil
}
