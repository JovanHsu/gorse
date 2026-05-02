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

	"github.com/chewxy/math32"
	"github.com/gorse-io/gorse/common/ann"
	"github.com/gorse-io/gorse/common/expression"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/samber/lo"
)

const (
	// InterestBasedRecommender is registered in recommend.go
)

// recommendInterestBased returns items similar to the user's interest profile
// computed from their positive feedback item embeddings.
func (r *Recommender) recommendInterestBased(ctx context.Context) ([]cache.Score, string, error) {
	// 1. Collect positive feedback items (up to 100)
	data.SortFeedbacks(r.userFeedback)
	var positiveItems []data.Item
	for _, fb := range r.userFeedback {
		if expression.MatchFeedbackTypeExpressions(r.config.DataSource.PositiveFeedbackTypes, fb.FeedbackType, fb.Value) {
			positiveItems = append(positiveItems, data.Item{ItemId: fb.ItemId})
			if len(positiveItems) >= 100 {
				break
			}
		}
	}
	if len(positiveItems) == 0 {
		return nil, InterestBasedRecommender, nil
	}

	// 2. Batch fetch items to get embeddings
	itemIds := lo.Map(positiveItems, func(it data.Item, _ int) string { return it.ItemId })
	items, err := r.dataClient.BatchGetItems(ctx, itemIds, data.GetOptions{SkipHidden: true})
	if err != nil {
		return nil, "", err
	}

	// 3. Collect embeddings from Labels["embedding"]
	var embeddings [][]float32
	var embeddingItems []data.Item
	for _, item := range items {
		if r.excludeSet.Contains(item.ItemId) {
			continue
		}
		emb := extractEmbedding(item.Labels)
		if emb != nil && len(emb) > 0 {
			embeddings = append(embeddings, emb)
			embeddingItems = append(embeddingItems, item)
		}
	}
	if len(embeddings) == 0 {
		return nil, InterestBasedRecommender, nil
	}

	// 4. Average embeddings to get user interest vector
	interestVector := averageEmbeddings(embeddings)

	// 5. Build ANN index with remaining items' embeddings
	hnsw := ann.NewHNSW(func(a, b []float32) float32 {
		return cosineSimilarity(a, b)
	})
	itemIndex := make(map[int]string) // ANN index -> itemId
	for _, item := range embeddingItems {
		emb := extractEmbedding(item.Labels)
		if emb != nil {
			idx := hnsw.Add(emb)
			itemIndex[idx] = item.ItemId
		}
	}

	// 6. Search ANN for top-50 similar items
	results := hnsw.SearchVector(interestVector, 50, false)

	// 7. Build scores from ANN results
	scores := make([]cache.Score, 0, len(results))
	for _, result := range results {
		itemId := itemIndex[result.A]
		if itemId == "" || r.excludeSet.Contains(itemId) {
			continue
		}
		scores = append(scores, cache.Score{
			Id:    itemId,
			Score: float64(result.B),
		})
	}

	// 8. Sort by similarity score descending
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Score > scores[j].Score
	})

	return scores, InterestBasedRecommender, nil
}

// extractEmbedding extracts a float32 embedding slice from item labels.
// Labels["embedding"] is expected to be []float32 or []float64.
func extractEmbedding(labels any) []float32 {
	if labels == nil {
		return nil
	}
	switch typed := labels.(type) {
	case map[string]any:
		emb, ok := typed["embedding"]
		if !ok {
			return nil
		}
		return anyToFloat32Slice(emb)
	case map[any]any:
		emb, ok := typed["embedding"]
		if !ok {
			return nil
		}
		return anyToFloat32Slice(emb)
	}
	return nil
}

// anyToFloat32Slice converts an any value to []float32.
func anyToFloat32Slice(v any) []float32 {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []float32:
		return s
	case []float64:
		out := make([]float32, len(s))
		for i, f := range s {
			out[i] = float32(f)
		}
		return out
	case []any:
		out := make([]float32, 0, len(s))
		for _, elem := range s {
			switch f := elem.(type) {
			case float64:
				out = append(out, float32(f))
			case float32:
				out = append(out, f)
			case int:
				out = append(out, float32(f))
			case int64:
				out = append(out, float32(f))
			}
		}
		return out
	}
	return nil
}

// averageEmbeddings computes the element-wise mean of a list of embeddings.
func averageEmbeddings(embeddings [][]float32) []float32 {
	if len(embeddings) == 0 {
		return nil
	}
	dim := len(embeddings[0])
	result := make([]float32, dim)
	for _, emb := range embeddings {
		for i := 0; i < dim && i < len(emb); i++ {
			result[i] += emb[i]
		}
	}
	for i := range result {
		result[i] /= float32(len(embeddings))
	}
	return result
}

// cosineSimilarity computes cosine similarity between two vectors.
// Returns 1 - cosine so that higher is better (used as "distance" in HNSW).
func cosineSimilarity(a, b []float32) float32 {
	var dot, normASq, normBSq float32
	for i := 0; i < len(a) && i < len(b); i++ {
		dot += a[i] * b[i]
		normASq += a[i] * a[i]
		normBSq += b[i] * b[i]
	}
	if normASq == 0 || normBSq == 0 {
		return 0
	}
	normA := math32.Sqrt(normASq)
	normB := math32.Sqrt(normBSq)
	// HNSW maximizes, so we return 1 - similarity (smaller distance = more similar)
	return 1 - dot/(normA*normB)
}
