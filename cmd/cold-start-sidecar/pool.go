package main

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/gorse-io/gorse/storage/data"
)

type Pool struct {
	store       *Store
	dataStore   data.Database
	tablePrefix string
}

func NewPool(store *Store, dataStore data.Database, tablePrefix string) *Pool {
	return &Pool{
		store:       store,
		dataStore:   dataStore,
		tablePrefix: tablePrefix,
	}
}

func (p *Pool) GetColdStartCandidates(ctx context.Context, userID string, n int) ([]string, error) {
	// Get all users and filter for high quality ones
	// We iterate with cursor-based pagination
	excludedItems, err := p.store.GetHighExposureItems(ctx, "all", 50)
	if err != nil {
		return nil, err
	}
	excludedSet := make(map[string]bool)
	for _, id := range excludedItems {
		excludedSet[id] = true
	}

	var candidates []CandidateItem
	cursor := ""
	now := time.Now()
	oneWeekAgo := now.AddDate(0, 0, -7)
	threeMonthsAgo := now.AddDate(0, -3, 0)

	for {
		var nextCursor string
		var users []data.User
		nextCursor, users, err = p.dataStore.GetUsers(ctx, cursor, 500)
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			if excludedSet[user.UserId] {
				continue
			}
			cand := p.userToCandidate(user)
			if cand.QualityScore < 0.7 || !cand.IsVerified {
				continue
			}
			// prefer recent users but not too recent
			if cand.Timestamp.Before(oneWeekAgo) && cand.Timestamp.After(threeMonthsAgo) {
				candidates = append(candidates, cand)
			} else if cand.Timestamp.Equal(time.Time{}) {
				// auto-created users: use profile completeness only
				candidates = append(candidates, cand)
			}
		}
		cursor = nextCursor
		if cursor == "" || len(candidates) >= n*5 {
			break
		}
	}

	// Sort by profile_completeness + recency
	sort.Slice(candidates, func(i, j int) bool {
		scoreI := candidates[i].ProfileCompleteness + p.recencyScore(candidates[i].Timestamp, now)
		scoreJ := candidates[j].ProfileCompleteness + p.recencyScore(candidates[j].Timestamp, now)
		return scoreI > scoreJ
	})

	if len(candidates) > n {
		candidates = candidates[:n]
	}
	result := make([]string, len(candidates))
	for i, c := range candidates {
		result[i] = c.ItemID
	}
	return result, nil
}

func (p *Pool) GetFirstScreenCandidates(ctx context.Context, userID string, n int) ([]string, error) {
	// Filter items exposed more than 50 times in last 7 days
	highExp, err := p.store.GetHighExposureItems(ctx, "all", 50)
	if err != nil {
		return nil, err
	}
	excludedSet := make(map[string]bool)
	for _, id := range highExp {
		excludedSet[id] = true
	}

	// Get latest items and filter
	var candidates []CandidateItem
	cursor := ""
	now := time.Now()
	threeMonthsAgo := now.AddDate(0, -3, 0)

	for {
		var nextCursor string
		var items []data.Item
		nextCursor, items, err = p.dataStore.GetItems(ctx, cursor, 500, &threeMonthsAgo)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if excludedSet[item.ItemId] {
				continue
			}
			if item.IsHidden {
				continue
			}
			candidates = append(candidates, CandidateItem{
				ItemID:    item.ItemId,
				Timestamp: item.Timestamp,
				Labels:    item.Labels,
			})
		}
		cursor = nextCursor
		if cursor == "" || len(candidates) >= n*3 {
			break
		}
	}

	// Sort by recency
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Timestamp.After(candidates[j].Timestamp)
	})

	if len(candidates) > n {
		candidates = candidates[:n]
	}
	result := make([]string, len(candidates))
	for i, c := range candidates {
		result[i] = c.ItemID
	}
	return result, nil
}

func (p *Pool) userToCandidate(user data.User) CandidateItem {
	cand := CandidateItem{
		ItemID:    user.UserId,
		Labels:    user.Labels,
		IsVerified: false,
		QualityScore: 0.0,
		ProfileCompleteness: 0.0,
	}
	if user.Labels == nil {
		return cand
	}
	var labels map[string]any
	switch v := user.Labels.(type) {
	case map[string]any:
		labels = v
	case []byte:
		_ = json.Unmarshal(v, &labels)
	case string:
		_ = json.Unmarshal([]byte(v), &labels)
	}
	if labels == nil {
		return cand
	}
	if v, ok := labels["quality_score"].(float64); ok {
		cand.QualityScore = v
	}
	if v, ok := labels["is_verified"].(bool); ok {
		cand.IsVerified = v
	}
	if v, ok := labels["profile_completeness"].(float64); ok {
		cand.ProfileCompleteness = v
	}
	if v, ok := labels["like_rate"].(float64); ok {
		cand.LikeRate = v
	}
	if v, ok := labels["block_rate"].(float64); ok {
		cand.BlockRate = v
	}
	return cand
}

func (p *Pool) recencyScore(ts time.Time, now time.Time) float64 {
	if ts.IsZero() {
		return 0
	}
	hours := now.Sub(ts).Hours()
	if hours <= 0 {
		return 1.0
	}
	return 1.0 / (1.0 + hours/24)
}
