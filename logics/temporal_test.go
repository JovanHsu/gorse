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

func TestIsItemActiveAtHour(t *testing.T) {
	tests := []struct {
		name  string
		labels any
		hour  int
		want  bool
	}{
		{
			name:  "nil labels",
			labels: nil,
			hour:  10,
			want:  false,
		},
		{
			name:  "empty map",
			labels: map[string]any{},
			hour:  10,
			want:  false,
		},
		{
			name:  "hour present - found",
			labels: map[string]any{"active_hours": []int{9, 10, 11}},
			hour:  10,
			want:  true,
		},
		{
			name:  "hour present - not found",
			labels: map[string]any{"active_hours": []int{9, 11, 12}},
			hour:  10,
			want:  false,
		},
		{
			name:  "float64 hours - found",
			labels: map[string]any{"active_hours": []float64{9, 10, 11}},
			hour:  10,
			want:  true,
		},
		{
			name:  "float64 hours - not found",
			labels: map[string]any{"active_hours": []float64{9, 11, 12}},
			hour:  10,
			want:  false,
		},
		{
			name:  "any hours - found",
			labels: map[string]any{"active_hours": []any{float64(9), float64(10), float64(11)}},
			hour:  10,
			want:  true,
		},
		{
			name:  "any hours - not found",
			labels: map[string]any{"active_hours": []any{float64(9), float64(11)}},
			hour:  10,
			want:  false,
		},
		{
			name:  "no active_hours key",
			labels: map[string]any{"photo_count": float64(5)},
			hour:  10,
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isItemActiveAtHour(tt.labels, tt.hour)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParsePhotoCountAndQuality(t *testing.T) {
	tests := []struct {
		name         string
		labels       any
		wantPC       int
		wantQS       float64
	}{
		{
			name:   "nil",
			labels: nil,
			wantPC: 0,
			wantQS: 0,
		},
		{
			name:   "empty",
			labels: map[string]any{},
			wantPC: 0,
			wantQS: 0,
		},
		{
			name:   "full data",
			labels: map[string]any{"photo_count": float64(5), "quality_score": float64(0.8)},
			wantPC: 5,
			wantQS: 0.8,
		},
		{
			name:   "partial - photo only",
			labels: map[string]any{"photo_count": float64(3)},
			wantPC: 3,
			wantQS: 0,
		},
		{
			name:   "partial - quality only",
			labels: map[string]any{"quality_score": float64(0.6)},
			wantPC: 0,
			wantQS: 0.6,
		},
		{
			name:   "int photo count",
			labels: map[string]any{"photo_count": 10, "quality_score": float64(0.5)},
			wantPC: 0, // int not handled, defaults to 0
			wantQS: 0.5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc, qs := parsePhotoCountAndQuality(tt.labels)
			assert.Equal(t, tt.wantPC, pc)
			assert.InDelta(t, tt.wantQS, qs, 0.001)
		})
	}
}

func TestExtractIntSlice(t *testing.T) {
	tests := []struct {
		name  string
		labels any
		key   string
		want  []int
	}{
		{
			name:  "nil",
			labels: nil,
			key:   "hours",
			want:  nil,
		},
		{
			name:  "[]int",
			labels: map[string]any{"hours": []int{9, 10, 11}},
			key:   "hours",
			want:  []int{9, 10, 11},
		},
		{
			name:  "[]int64",
			labels: map[string]any{"hours": []int64{9, 10, 11}},
			key:   "hours",
			want:  []int{9, 10, 11},
		},
		{
			name:  "[]float64",
			labels: map[string]any{"hours": []float64{9, 10, 11}},
			key:   "hours",
			want:  []int{9, 10, 11},
		},
		{
			name:  "[]any float64",
			labels: map[string]any{"hours": []any{float64(9), float64(10)}},
			key:   "hours",
			want:  []int{9, 10},
		},
		{
			name:  "missing key",
			labels: map[string]any{"other": []int{1, 2}},
			key:   "hours",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractIntSlice(tt.labels, tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAnyToIntSlice(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []int
	}{
		{
			name: "nil",
			v:    nil,
			want: nil,
		},
		{
			name: "[]int",
			v:    []int{1, 2, 3},
			want: []int{1, 2, 3},
		},
		{
			name: "[]int64",
			v:    []int64{1, 2, 3},
			want: []int{1, 2, 3},
		},
		{
			name: "[]float64",
			v:    []float64{1, 2, 3},
			want: []int{1, 2, 3},
		},
		{
			name: "[]any mixed",
			v:    []any{float64(1), float64(2), float64(3)},
			want: []int{1, 2, 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anyToIntSlice(tt.v)
			assert.Equal(t, tt.want, got)
		})
	}
}
