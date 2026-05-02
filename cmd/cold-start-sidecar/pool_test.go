package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorse-io/gorse/storage/data"
	"github.com/stretchr/testify/assert"
)

func TestParseN(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{"default", "", 20},
		{"valid", "?n=10", 10},
		{"invalid", "?n=abc", 20},
		{"zero", "?n=0", 20},
		{"negative", "?n=-5", 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/cold_start_pool/u1"+tt.query, nil)
			n := parseN(req)
			assert.Equal(t, tt.expected, n)
		})
	}
}

func TestRecencyScore(t *testing.T) {
	pool := &Pool{}
	now := time.Now()

	tests := []struct {
		name     string
		ts       time.Time
		minScore float64
	}{
		{"zero time", time.Time{}, 0.0},
		{"now", now, 1.0},
		{"1 hour ago", now.Add(-1 * time.Hour), 0.4},
		{"1 day ago", now.AddDate(0, 0, -1), 0.03},
		{"7 days ago", now.AddDate(0, 0, -7), 0.005},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := pool.recencyScore(tt.ts, now)
			assert.GreaterOrEqual(t, score, tt.minScore)
		})
	}
}

func TestUserToCandidate(t *testing.T) {
	pool := &Pool{}

	// Test nil labels
	g := "M"
	cand := pool.userToCandidate(data.User{UserId: "test", Gender: &g, Labels: nil})
	assert.Equal(t, 0.0, cand.QualityScore)
	assert.False(t, cand.IsVerified)

	// Test map labels
	cand = pool.userToCandidate(data.User{
		UserId: "test2",
		Gender: &g,
		Labels: map[string]any{
			"quality_score":        0.85,
			"is_verified":          true,
			"profile_completeness": 0.9,
			"like_rate":            0.12,
			"block_rate":           0.02,
		},
	})
	assert.Equal(t, 0.85, cand.QualityScore)
	assert.True(t, cand.IsVerified)
	assert.Equal(t, 0.9, cand.ProfileCompleteness)
	assert.Equal(t, 0.12, cand.LikeRate)
	assert.Equal(t, 0.02, cand.BlockRate)

	// Test JSON string labels
	cand = pool.userToCandidate(data.User{
		UserId: "test3",
		Gender: &g,
		Labels: `{"quality_score": 0.75, "is_verified": true, "profile_completeness": 0.5}`,
	})
	assert.Equal(t, 0.75, cand.QualityScore)
	assert.True(t, cand.IsVerified)
	assert.Equal(t, 0.5, cand.ProfileCompleteness)
}
