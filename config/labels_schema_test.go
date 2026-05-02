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

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToUserLabels(t *testing.T) {
	raw := map[string]any{
		"age":          float64(25),
		"city":         "beijing",
		"purpose":      "relationship",
		"quality_score": 0.9,
		"is_verified":  float64(1),
		"tier":         "vip",
		"pref_age_min": float64(22),
		"pref_age_max": float64(34),
	}
	u, ok := ToUserLabels(raw)
	assert.True(t, ok)
	assert.Equal(t, 25, u.Age)
	assert.Equal(t, "beijing", u.City)
	assert.Equal(t, "relationship", u.Purpose)
	assert.Equal(t, 0.9, u.QualityScore)
	assert.Equal(t, "vip", u.Tier)
	assert.Equal(t, 22, u.PrefAgeMin)
	assert.Equal(t, 34, u.PrefAgeMax)
}

func TestToUserLabels_Nil(t *testing.T) {
	_, ok := ToUserLabels(nil)
	assert.False(t, ok)
}

func TestToItemLabels(t *testing.T) {
	raw := map[string]any{
		"age":           float64(28),
		"city":          "shanghai",
		"quality_score":  0.8,
		"is_verified":   float64(1),
		"like_rate":     0.42,
		"block_rate":    0.02,
		"active_hours":  []any{22.0, 23.0, 0.0, 1.0, 2.0},
	}
	i, ok := ToItemLabels(raw)
	assert.True(t, ok)
	assert.Equal(t, 28, i.Age)
	assert.Equal(t, "shanghai", i.City)
	assert.Equal(t, 0.8, i.QualityScore)
	assert.Equal(t, 0.42, i.LikeRate)
	assert.Equal(t, 0.02, i.BlockRate)
	assert.Equal(t, []int{22, 23, 0, 1, 2}, i.ActiveHours)
}

func TestUserLabels_ToMap(t *testing.T) {
	u := UserLabels{
		Age:           28,
		City:          "beijing",
		Purpose:       "relationship",
		QualityScore:  0.9,
		IsVerified:    true,
		Tier:          "vip",
		PrefAgeMin:    22,
		PrefAgeMax:    34,
	}
	m := u.ToMap()
	assert.Equal(t, 28, m["age"])             // int stored as int
	assert.Equal(t, "beijing", m["city"])
	assert.Equal(t, "relationship", m["purpose"])
	assert.Equal(t, 0.9, m["quality_score"])
	assert.Equal(t, true, m["is_verified"])
	assert.Equal(t, "vip", m["tier"])
	assert.Equal(t, 22, m["pref_age_min"])
	assert.Equal(t, 34, m["pref_age_max"])
	// Zero-value fields should be absent
	_, hasAge := m["age"]
	assert.True(t, hasAge)
	_, hasCity := m["city"]
	assert.True(t, hasCity)
	_, hasQuality := m["quality_score"]
	assert.True(t, hasQuality)
}

func TestItemLabels_ToMap(t *testing.T) {
	i := ItemLabels{
		Age:          30,
		QualityScore: 0.85,
		IsVerified:   true,
		LikeRate:     0.38,
		BlockRate:    0.01,
		ActiveHours:  []int{9, 10, 11, 12, 13, 14},
	}
	m := i.ToMap()
	assert.Equal(t, 30, m["age"])
	assert.Equal(t, 0.85, m["quality_score"])
	assert.Equal(t, true, m["is_verified"])
	assert.Equal(t, 0.38, m["like_rate"])
	assert.Equal(t, 0.01, m["block_rate"])
	assert.Equal(t, []int{9, 10, 11, 12, 13, 14}, m["active_hours"])
}

func TestUserLabels_Validate(t *testing.T) {
	err := UserLabels{Age: 150}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "age")

	err = UserLabels{RightSwipeRate: 1.5}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "right_swipe_rate")

	err = UserLabels{PrefAgeMin: 40, PrefAgeMax: 20}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pref_age_range")

	// MEDIUM #13: pref_age_min=0 is valid (means "no minimum")
	err = UserLabels{PrefAgeMin: 0, PrefAgeMax: 18}.Validate()
	assert.NoError(t, err)

	err = UserLabels{Gender: "unknown"}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gender")

	err = UserLabels{StatsUpdatedAt: "not-a-date"}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stats_updated_at")

	err = UserLabels{Age: 25, City: "beijing"}.Validate()
	assert.NoError(t, err)
}

func TestItemLabels_Validate(t *testing.T) {
	err := ItemLabels{Age: 200}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "age")

	err = ItemLabels{LikeRate: 2.0}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "like_rate")

	err = ItemLabels{RiskScore: -0.1}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "risk_score")

	err = ItemLabels{ActiveHours: []int{25}}.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "active_hours")

	err = ItemLabels{Age: 30, QualityScore: 0.8}.Validate()
	assert.NoError(t, err)
}

func TestLabelValidationError(t *testing.T) {
	err := &LabelValidationError{Field: "age", Value: 200, Reason: "must be between 0 and 120"}
	assert.Equal(t, "invalid age: must be between 0 and 120", err.Error())
}
