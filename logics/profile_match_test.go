// Copyright 2025 gorse Project Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logics

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/gorse-io/gorse/config"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestHaversineKm(t *testing.T) {
	tests := []struct {
		name     string
		lat1, lon1, lat2, lon2 float64
		wantKm   float64
		epsilon  float64
	}{
		{
			name:    "beijing to shanghai",
			lat1:    39.9042, lon1: 116.4074, // Beijing
			lat2:    31.2304, lon2: 121.4737, // Shanghai
			wantKm:  1068.0,
			epsilon: 5.0, // within 5km
		},
		{
			name:    "same point",
			lat1:    31.2304, lon1: 121.4737,
			lat2:    31.2304, lon2: 121.4737,
			wantKm:  0.0,
			epsilon: 0.01,
		},
		{
			name:    "beijing to newyork",
			lat1:    39.9042, lon1: 116.4074, // Beijing
			lat2:    40.7128, lon2: -74.0060,  // New York
			wantKm:  11000.0,
			epsilon: 100.0,
		},
		{
			name:    "short distance 10km",
			lat1:    31.2, lon1: 121.4,
			lat2:    31.3, lon2: 121.5,
			wantKm:  13.0,
			epsilon: 2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := haversineKm(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			assert.InDelta(t, tt.wantKm, got, tt.epsilon,
				"haversineKm(%f, %f, %f, %f) = %f, want ~%f",
				tt.lat1, tt.lon1, tt.lat2, tt.lon2, got, tt.wantKm)
		})
	}
}

func TestHaversineKm_Accuracy(t *testing.T) {
	// Known distance: equator circumference ~40075 km
	// Moving 1 degree longitude at equator ≈ 111.32 km
	d := haversineKm(0, 0, 0, 1)
	assert.InDelta(t, 111.32, d, 1.0)

	// Moving 1 degree latitude ≈ 111.32 km everywhere
	d2 := haversineKm(0, 0, 1, 0)
	assert.InDelta(t, 111.32, d2, 1.0)
}

func TestHaversineKm_Antipodal(t *testing.T) {
	// Antipodal points are ~20015 km apart (half equator)
	d := haversineKm(0, 0, 0, 180)
	assert.InDelta(t, 20015, d, 50)

	// North pole to south pole
	d2 := haversineKm(90, 0, -90, 0)
	assert.InDelta(t, 20000, d2, 50)
}

func TestHaversineKm_NoPanicOnZero(t *testing.T) {
	// Zero coordinates should not panic
	d := haversineKm(0, 0, 0, 0)
	assert.InDelta(t, 0.0, d, 0.01)

	// Edge case: crossing the date line
	d2 := haversineKm(0, 179, 0, -179)
	assert.True(t, d2 < 500, "crossing date line should be short")
}

// Verify haversineKm satisfies triangle inequality (distance metric property)
func TestHaversineKm_TriangleInequality(t *testing.T) {
	aLat, aLon := 31.2, 121.4
	bLat, bLon := 31.3, 121.5
	cLat, cLon := 31.4, 121.6

	ab := haversineKm(aLat, aLon, bLat, bLon)
	bc := haversineKm(bLat, bLon, cLat, cLon)
	ac := haversineKm(aLat, aLon, cLat, cLon)

	// Triangle inequality: AC <= AB + BC
	assert.True(t, ac <= ab+bc+0.001,
		"triangle inequality violated: %f > %f + %f", ac, ab, bc)
}

// Verify symmetry: distance A->B == B->A
func TestHaversineKm_Symmetry(t *testing.T) {
	for _, tc := range []struct {
		lat1, lon1, lat2, lon2 float64
	}{
		{31.2, 121.4, 31.3, 121.5},
		{39.9, 116.4, 31.2, 121.4},
		{0, 0, 45, 45},
		{90, 0, -90, 180},
	} {
		d1 := haversineKm(tc.lat1, tc.lon1, tc.lat2, tc.lon2)
		d2 := haversineKm(tc.lat2, tc.lon2, tc.lat1, tc.lon1)
		assert.InDelta(t, d1, d2, 0.001,
			"haversine not symmetric: %f != %f", d1, d2)
	}
}

func TestHaversineKm_NonNegativity(t *testing.T) {
	// Distance must always be >= 0
	pairs := [][4]float64{
		{0, 0, 0, 0},
		{90, 0, -90, 0},
		{31.2, 121.4, 39.9, 116.4},
		{0, 0, 0, 180},
	}
	for _, p := range pairs {
		d := haversineKm(p[0], p[1], p[2], p[3])
		assert.True(t, d >= 0, "distance should be non-negative: %f", d)
		assert.False(t, math.IsNaN(d), "distance should not be NaN")
		assert.False(t, math.IsInf(d, 0), "distance should not be infinite")
	}
}

// distanceIntegrationTest wraps SQLite-based profile_match tests.
type distanceIntegrationTest struct {
	suite.Suite
	dataClient  data.Database
	cacheClient cache.Database
}

func TestProfileMatch_DistanceFiltering(t *testing.T) {
	suite.Run(t, new(distanceIntegrationTest))
}

func (s *distanceIntegrationTest) SetupSuite() {
	var err error
	s.dataClient, err = data.Open(fmt.Sprintf("sqlite://%s/data.db", s.T().TempDir()), "")
	s.NoError(err)
	s.cacheClient, err = cache.Open(fmt.Sprintf("sqlite://%s/cache.db", s.T().TempDir()), "")
	s.NoError(err)
	s.NoError(s.dataClient.Init())
	s.NoError(s.cacheClient.Init())
}

func (s *distanceIntegrationTest) TearDownSuite() {
	s.NoError(s.dataClient.Close())
	s.NoError(s.cacheClient.Close())
}

func (s *distanceIntegrationTest) insertUserWithLabels(userId string, labels map[string]any) {
	s.NoError(s.dataClient.BatchInsertUsers(s.T().Context(), []data.User{{
		UserId: userId,
		Labels: labels,
	}}))
}

func (s *distanceIntegrationTest) insertItemWithLabels(itemId, category string, labels map[string]any) {
	s.NoError(s.dataClient.BatchInsertItems(s.T().Context(), []data.Item{{
		ItemId:     itemId,
		Categories: []string{category},
		Labels:     labels,
		Timestamp:  time.Now(),
	}}))
}

// TestDistanceFiltering_NearUsers returns users within distance.
func (s *distanceIntegrationTest) TestDistanceFiltering_NearUsers() {
	// User in Beijing (39.9, 116.4) wants partners within 20km.
	s.insertUserWithLabels("alice", map[string]any{
		"gender": "F",
		"latitude": 39.9042,
		"longitude": 116.4074,
		"pref_age_min":       20,
		"pref_age_max":       40,
		"pref_max_distance_km": 20,
	})

	// Candidates:
	//  - near_beijing: (39.92, 116.45) — ~5km away — within 20km ✅
	//  - mid_distance: (40.10, 116.80) — ~36km away — outside 20km ❌
	//  - far_away:    (31.23, 121.47) — ~1068km away — outside 20km ❌

	nearLat, nearLon := 39.92, 116.45     // ~5km from Beijing
	midLat, midLon := 40.10, 116.80       // ~36km from Beijing
	farLat, farLon := 31.23, 121.47       // ~1068km from Beijing

	s.insertItemWithLabels("near_beijing", "M", map[string]any{
		"gender": "M", "age": 28,
		"latitude": nearLat, "longitude": nearLon,
	})
	s.insertItemWithLabels("mid_distance", "M", map[string]any{
		"gender": "M", "age": 30,
		"latitude": midLat, "longitude": midLon,
	})
	s.insertItemWithLabels("far_away", "M", map[string]any{
		"gender": "M", "age": 25,
		"latitude": farLat, "longitude": farLon,
	})

	cfg := config.RecommendConfig{
		ProfileMatch: config.ProfileMatchConfig{
			MinAge: 18, MaxAge: 65,
		},
	}
	rec, err := NewRecommender(cfg, s.cacheClient, s.dataClient, true, "alice", nil)
	s.NoError(err)
	rec.config = cfg

	scores, digest, err := rec.recommendProfileMatch(s.T().Context())
	s.NoError(err)
	s.Equal("profile_match", digest)

	// near_beijing should be in results; mid and far should be filtered out.
	resultIds := make([]string, len(scores))
	for i, sc := range scores {
		resultIds[i] = sc.Id
	}

	s.Contains(resultIds, "near_beijing", "near_beijing should be within 20km")
	s.NotContains(resultIds, "mid_distance", "mid_distance (~36km) should be filtered out")
	s.NotContains(resultIds, "far_away", "far_away (~1068km) should be filtered out")
}

// TestDistanceFiltering_NoDistancePreference returns all candidates.
func (s *distanceIntegrationTest) TestDistanceFiltering_NoDistancePreference() {
	// User has no distance preference (pref_max_distance_km = 0).
	s.insertUserWithLabels("bob", map[string]any{
		"gender": "M",
		"latitude": 31.23,
		"longitude": 121.47,
		"pref_age_min": 20,
		"pref_age_max": 40,
		// No pref_max_distance_km set
	})

	s.insertItemWithLabels("shanghai_user", "F", map[string]any{
		"gender": "F", "age": 25,
		"latitude": 31.23, "longitude": 121.47, // Shanghai
	})
	s.insertItemWithLabels("beijing_user", "F", map[string]any{
		"gender": "F", "age": 28,
		"latitude": 39.90, "longitude": 116.41, // Beijing ~1068km
	})

	cfg := config.RecommendConfig{
		ProfileMatch: config.ProfileMatchConfig{
			MinAge: 18, MaxAge: 65,
		},
	}
	rec, err := NewRecommender(cfg, s.cacheClient, s.dataClient, true, "bob", nil)
	s.NoError(err)
	rec.config = cfg

	scores, _, err := rec.recommendProfileMatch(s.T().Context())
	s.NoError(err)

	resultIds := make([]string, len(scores))
	for i, sc := range scores {
		resultIds[i] = sc.Id
	}

	// Both should appear when no distance filter is set.
	s.Contains(resultIds, "shanghai_user")
	s.Contains(resultIds, "beijing_user")
}

// TestDistanceFiltering_UserHasNoLocation skips distance filter.
func (s *distanceIntegrationTest) TestDistanceFiltering_UserHasNoLocation() {
	// User has no lat/lon set — distance filter should be skipped.
	s.insertUserWithLabels("charlie", map[string]any{
		"gender": "F",
		// No latitude/longitude
		"pref_age_min": 20,
		"pref_age_max": 40,
		"pref_max_distance_km": 50, // set but user has no location
	})

	s.insertItemWithLabels("nearby_no_loc", "M", map[string]any{
		"gender": "M", "age": 28,
		"latitude": 39.9, "longitude": 116.4,
	})

	cfg := config.RecommendConfig{
		ProfileMatch: config.ProfileMatchConfig{
			MinAge: 18, MaxAge: 65,
		},
	}
	rec, err := NewRecommender(cfg, s.cacheClient, s.dataClient, true, "charlie", nil)
	s.NoError(err)
	rec.config = cfg

	_, _, err = rec.recommendProfileMatch(s.T().Context())
	s.NoError(err)

	// Should not panic; the distance filter is skipped since user has no location.
}

// TestDistanceFiltering_CandidateHasNoLocation passes through.
func (s *distanceIntegrationTest) TestDistanceFiltering_CandidateHasNoLocation() {
	// User has location; candidate does not — candidate should NOT be filtered out.
	s.insertUserWithLabels("diana", map[string]any{
		"gender": "F",
		"latitude": 31.23,
		"longitude": 121.47,
		"pref_age_min": 20,
		"pref_age_max": 40,
		"pref_max_distance_km": 5, // very small, would filter everyone
	})

	// Candidate with no location data.
	s.insertItemWithLabels("no_location_cand", "M", map[string]any{
		"gender": "M", "age": 28,
		// No latitude/longitude — should NOT be filtered out.
	})

	cfg := config.RecommendConfig{
		ProfileMatch: config.ProfileMatchConfig{
			MinAge: 18, MaxAge: 65,
		},
	}
	rec, err := NewRecommender(cfg, s.cacheClient, s.dataClient, true, "diana", nil)
	s.NoError(err)
	rec.config = cfg

	scores, _, err := rec.recommendProfileMatch(s.T().Context())
	s.NoError(err)

	resultIds := make([]string, len(scores))
	for i, sc := range scores {
		resultIds[i] = sc.Id
	}

	// Candidate without location should NOT be filtered out.
	s.Contains(resultIds, "no_location_cand",
		"candidate without location should pass through distance filter")
}

// TestDistanceFiltering_ExactBoundaryAtMaxDistance includes at-boundary.
func (s *distanceIntegrationTest) TestDistanceFiltering_ExactBoundaryAtMaxDistance() {
	// Beijing (39.9, 116.4) + 50km max.
	// Tianjin (39.14, 117.20) ≈ 95km — filtered out.
	// Langfang (39.52, 116.68) ≈ 28km — included.
	s.insertUserWithLabels("eve", map[string]any{
		"gender": "F",
		"latitude": 39.9042,
		"longitude": 116.4074,
		"pref_age_min": 20,
		"pref_age_max": 40,
		"pref_max_distance_km": 50,
	})

	langfangLat, langfangLon := 39.52, 116.68   // ~28km from Beijing
	tianjinLat, tianjinLon := 39.14, 117.20      // ~95km from Beijing

	s.insertItemWithLabels("langfang_user", "M", map[string]any{
		"gender": "M", "age": 30,
		"latitude": langfangLat, "longitude": langfangLon,
	})
	s.insertItemWithLabels("tianjin_user", "M", map[string]any{
		"gender": "M", "age": 28,
		"latitude": tianjinLat, "longitude": tianjinLon,
	})

	cfg := config.RecommendConfig{
		ProfileMatch: config.ProfileMatchConfig{
			MinAge: 18, MaxAge: 65,
		},
	}
	rec, err := NewRecommender(cfg, s.cacheClient, s.dataClient, true, "eve", nil)
	s.NoError(err)
	rec.config = cfg

	scores, _, err := rec.recommendProfileMatch(s.T().Context())
	s.NoError(err)

	resultIds := make([]string, len(scores))
	for i, sc := range scores {
		resultIds[i] = sc.Id
	}

	s.Contains(resultIds, "langfang_user", "langfang (~28km) should be within 50km")
	s.NotContains(resultIds, "tianjin_user", "tianjin (~95km) should be outside 50km")
}
