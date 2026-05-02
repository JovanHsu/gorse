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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAverageEmbeddings(t *testing.T) {
	tests := []struct {
		name      string
		embeddings [][]float32
		want      []float32
	}{
		{
			name:      "single embedding",
			embeddings: [][]float32{{1.0, 2.0, 3.0}},
			want:      []float32{1.0, 2.0, 3.0},
		},
		{
			name:      "two embeddings",
			embeddings: [][]float32{{1.0, 2.0}, {3.0, 4.0}},
			want:      []float32{2.0, 3.0},
		},
		{
			name:      "three embeddings",
			embeddings: [][]float32{{1.0, 2.0}, {3.0, 4.0}, {5.0, 6.0}},
			want:      []float32{3.0, 4.0},
		},
		{
			name:      "empty",
			embeddings: [][]float32{},
			want:      nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := averageEmbeddings(tt.embeddings)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.InDeltaSlice(t, tt.want, got, 0.001)
			}
		})
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a    []float32
		b    []float32
		want float32
	}{
		{
			name: "identical vectors",
			a:    []float32{1.0, 0.0},
			b:    []float32{1.0, 0.0},
			want: 0, // 1 - 1 = 0
		},
		{
			name: "orthogonal vectors",
			a:    []float32{1.0, 0.0},
			b:    []float32{0.0, 1.0},
			want: 1, // 1 - 0 = 1
		},
		{
			name: "opposite vectors",
			a:    []float32{1.0, 0.0},
			b:    []float32{-1.0, 0.0},
			want: 2, // 1 - (-1) = 2
		},
		{
			name: "partial overlap",
			a:    []float32{1.0, 1.0},
			b:    []float32{1.0, 0.0},
			want: 0.2929, // 1 - (1/sqrt(2)) ≈ 0.2929
		},
		{
			name: "zero vector a",
			a:    []float32{0.0, 0.0},
			b:    []float32{1.0, 1.0},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			assert.InDelta(t, tt.want, got, 0.01)
		})
	}
}

func TestExtractEmbedding(t *testing.T) {
	tests := []struct {
		name   string
		labels any
		want   []float32
	}{
		{
			name:   "nil labels",
			labels: nil,
			want:   nil,
		},
		{
			name:   "empty map",
			labels: map[string]any{},
			want:   nil,
		},
		{
			name:   "float32 slice",
			labels: map[string]any{"embedding": []float32{0.1, 0.2, 0.3}},
			want:   []float32{0.1, 0.2, 0.3},
		},
		{
			name:   "float64 slice",
			labels: map[string]any{"embedding": []float64{0.1, 0.2, 0.3}},
			want:   []float32{0.1, 0.2, 0.3},
		},
		{
			name:   "[]any float64",
			labels: map[string]any{"embedding": []any{float64(0.1), float64(0.2), float64(0.3)}},
			want:   []float32{0.1, 0.2, 0.3},
		},
		{
			name:   "[]any mixed",
			labels: map[string]any{"embedding": []any{float64(0.1), 0.2, 3}},
			want:   []float32{0.1, 0.2, 3.0},
		},
		{
			name:   "no embedding key",
			labels: map[string]any{"other": 123},
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEmbedding(tt.labels)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.InDeltaSlice(t, tt.want, got, 0.001)
			}
		})
	}
}

func TestAnyToFloat32Slice(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []float32
	}{
		{
			name: "nil",
			v:    nil,
			want: nil,
		},
		{
			name: "[]float32",
			v:    []float32{1.0, 2.0},
			want: []float32{1.0, 2.0},
		},
		{
			name: "[]float64",
			v:    []float64{1.0, 2.0},
			want: []float32{1.0, 2.0},
		},
		{
			name: "[]any float64",
			v:    []any{float64(1.0), float64(2.0)},
			want: []float32{1.0, 2.0},
		},
		{
			name: "[]any int",
			v:    []any{1, 2, 3},
			want: []float32{1.0, 2.0, 3.0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anyToFloat32Slice(tt.v)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.InDeltaSlice(t, tt.want, got, 0.001)
			}
		})
	}
}
