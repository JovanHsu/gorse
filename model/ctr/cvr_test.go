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
	"testing"

	"github.com/gorse-io/gorse/storage/data"
	"github.com/stretchr/testify/assert"
)

// mockDataClientForCVR implements a minimal data.Database for testing CVR.
type mockDataClientForCVR struct {
	data.Database
	feedbackByItem map[string][]data.Feedback
}

func (m *mockDataClientForCVR) GetItemFeedback(ctx context.Context, itemId string, feedbackTypes ...string) ([]data.Feedback, error) {
	var result []data.Feedback
	for _, fb := range m.feedbackByItem[itemId] {
		for _, ft := range feedbackTypes {
			if fb.FeedbackType == ft {
				result = append(result, fb)
				break
			}
		}
	}
	return result, nil
}

func fb(itemId, feedbackType string) data.Feedback {
	return data.Feedback{FeedbackKey: data.FeedbackKey{ItemId: itemId, FeedbackType: feedbackType}}
}

func TestCVRModel_ComputeCVRScores(t *testing.T) {
	tests := []struct {
		name           string
		items          []*data.Item
		feedbackByItem map[string][]data.Feedback
		wantScores     map[string]float64
	}{
		{
			name:  "empty items",
			items: []*data.Item{},
			feedbackByItem: map[string][]data.Feedback{
				"item1": {fb("item1", "match")},
			},
			wantScores: map[string]float64{},
		},
		{
			name: "no feedback",
			items: []*data.Item{
				{ItemId: "item1"},
				{ItemId: "item2"},
			},
			feedbackByItem: map[string][]data.Feedback{},
			wantScores: map[string]float64{
				"item1": 0,
				"item2": 0,
			},
		},
		{
			name: "some matches no chats",
			items: []*data.Item{
				{ItemId: "item1"},
			},
			feedbackByItem: map[string][]data.Feedback{
				"item1": {fb("item1", "match"), fb("item1", "match"), fb("item1", "match")},
			},
			wantScores: map[string]float64{
				"item1": 0, // chat=0, match=3 -> 0/3=0
			},
		},
		{
			name: "some matches and chats",
			items: []*data.Item{
				{ItemId: "item1"},
			},
			feedbackByItem: map[string][]data.Feedback{
				"item1": {fb("item1", "match"), fb("item1", "match"), fb("item1", "chat")},
			},
			wantScores: map[string]float64{
				"item1": 0.5, // chat=1, match=2 -> 1/2=0.5
			},
		},
		{
			name: "multiple items",
			items: []*data.Item{
				{ItemId: "item1"},
				{ItemId: "item2"},
				{ItemId: "item3"},
			},
			feedbackByItem: map[string][]data.Feedback{
				"item1": {fb("item1", "match"), fb("item1", "chat")},
				"item2": {fb("item2", "match"), fb("item2", "match"), fb("item2", "chat"), fb("item2", "chat")},
			},
			wantScores: map[string]float64{
				"item1": 1.0, // chat=1, match=1 -> 1
				"item2": 1.0, // chat=2, match=2 -> 1
				"item3": 0,   // no feedback
			},
		},
		{
			name: "ignores other feedback types",
			items: []*data.Item{
				{ItemId: "item1"},
			},
			feedbackByItem: map[string][]data.Feedback{
				"item1": {fb("item1", "match"), fb("item1", "like"), fb("item1", "chat"), fb("item1", "view")},
			},
			wantScores: map[string]float64{
				"item1": 1.0, // chat=1, match=1 -> 1
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockDataClientForCVR{feedbackByItem: tt.feedbackByItem}
			model := NewCVRModel(mock)
			scores, err := model.ComputeCVRScores(context.Background(), tt.items)
			assert.NoError(t, err)
			for itemId, want := range tt.wantScores {
				got, ok := scores[itemId]
				assert.True(t, ok, "item %s not in scores", itemId)
				assert.InDelta(t, want, got, 0.001, "item %s cvr mismatch", itemId)
			}
		})
	}
}
