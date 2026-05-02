package main

import (
	"context"
	"sort"
	"strconv"
	"time"
)

func (s *Store) GetPoolCoverage(ctx context.Context) (*PoolCoverageResponse, error) {
	poolKeys, err := s.ScanKeys(ctx, "recall:coverage:*")
	if err != nil {
		return nil, err
	}

	// First pass: collect per-pool item counts
	rawCounts := make(map[string]int64)
	var totalCount int64
	for _, key := range poolKeys {
		// key format: recall:coverage:{pool_name}
		poolName := key[16:] // remove "recall:coverage:" prefix
		vals, err := s.HGetAll(ctx, key)
		if err != nil {
			continue
		}
		var poolTotal int64
		for _, countStr := range vals {
			cnt, _ := strconv.ParseInt(countStr, 10, 64)
			poolTotal += cnt
		}
		rawCounts[poolName] = poolTotal
		totalCount += poolTotal
	}

	// Second pass: compute coverage as fraction of total
	pools := make(map[string]PoolStat)
	for poolName, poolTotal := range rawCounts {
		var coverage float64
		if totalCount > 0 {
			coverage = float64(poolTotal) / float64(totalCount)
		}
		pools[poolName] = PoolStat{Count: poolTotal, Coverage: coverage}
	}

	return &PoolCoverageResponse{
		Timestamp: time.Now().UTC(),
		Pools:     pools,
	}, nil
}

func (s *Store) GetFatigueRate(ctx context.Context) (*FatigueRateResponse, error) {
	keys, err := s.ScanKeys(ctx, "fatigue:*")
	if err != nil {
		return nil, err
	}

	sevenDaysAgo := time.Now().AddDate(0, 0, -7).Unix()
	var fatiguedUsers int64

	for _, key := range keys {
		lastMatchStr, err := s.HGet(ctx, key, "last_match_at")
		if err != nil {
			continue
		}
		lastMatch, _ := strconv.ParseInt(lastMatchStr, 10, 64)
		if lastMatch >= sevenDaysAgo {
			fatiguedUsers++
		}
	}

	totalUsers := int64(len(keys))
	var rate float64
	if totalUsers > 0 {
		rate = float64(fatiguedUsers) / float64(totalUsers)
	}

	return &FatigueRateResponse{
		Timestamp:     time.Now().UTC(),
		FatigueRate:   rate,
		FatiguedUsers: fatiguedUsers,
		TotalUsers:    totalUsers,
	}, nil
}

func (s *Store) GetSDBalance(ctx context.Context) (*SDBalanceResponse, error) {
	keys, err := s.ScanKeys(ctx, "sd:exposure:female:*")
	if err != nil {
		return nil, err
	}

	var totalExposure int64
	var topExposures []int64

	for _, key := range keys {
		val, err := s.redis.Get(ctx, key).Int64()
		if err != nil {
			continue
		}
		totalExposure += val
		topExposures = append(topExposures, val)
	}

	sort.Slice(topExposures, func(i, j int) bool {
		return topExposures[i] > topExposures[j]
	})

	var top10PctExposure int64
	if len(topExposures) > 0 {
		topN := (len(topExposures) + 9) / 10
		if topN > len(topExposures) {
			topN = len(topExposures)
		}
		for i := 0; i < topN; i++ {
			top10PctExposure += topExposures[i]
		}
	}

	var concentration float64
	if totalExposure > 0 {
		concentration = float64(top10PctExposure) / float64(totalExposure)
	}

	status := "balanced"
	if concentration > 0.40 {
		status = "imbalanced"
	}

	return &SDBalanceResponse{
		Timestamp:                   time.Now().UTC(),
		FemaleExposureConcentration: concentration,
		RecommendedThreshold:        0.40,
		Status:                      status,
	}, nil
}
