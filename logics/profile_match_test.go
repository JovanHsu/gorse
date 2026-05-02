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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
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
