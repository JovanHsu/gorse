package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

func (s *Server) generateReport(ctx context.Context, experiment string) (*ReportResponse, error) {
	groups := []string{"control", "A", "B"}
	report := &ReportResponse{
		Experiment: experiment,
		Groups:     make(map[string]GroupMetrics),
	}

	controlMetrics := make(map[string]float64)
	groupMetrics := make(map[string]map[string]float64)

	for _, g := range groups {
		ms, err := s.getAllMetrics(ctx, experiment, g)
		if err != nil && err != redis.Nil {
			return nil, err
		}
		groupMetrics[g] = ms

		if g == "control" {
			for k, v := range ms {
				controlMetrics[k] = v
			}
		}
	}

	for _, g := range groups {
		ms := groupMetrics[g]
		gm := GroupMetrics{}
		sampleSize, err := getSampleSize(ctx, s.rdb, experiment, g)
		if err != nil {
			sampleSize = 0
		}
		gm.SampleSize = sampleSize

		for metric, val := range ms {
			if g == "control" {
				gm.MatchRate = val
				report.Groups[g] = gm
			} else {
				if ctrlVal, ok := controlMetrics[metric]; ok && ctrlVal > 0 {
					lift := ((val - ctrlVal) / ctrlVal) * 100
					gm.MatchRate = val
					gm.Lift = fmt.Sprintf("%+.1f%%", lift)
					report.Groups[g] = gm
					break
				}
			}
		}

		if _, ok := report.Groups[g]; !ok {
			report.Groups[g] = gm
		}
	}

	return report, nil
}

func (s *Server) getAllMetrics(ctx context.Context, experiment, group string) (map[string]float64, error) {
	pattern := fmt.Sprintf("ab:metrics:%s:%s:sum:*", experiment, group)
	var sumKeys []string
	iter := s.rdb.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		sumKeys = append(sumKeys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}

	metrics := make(map[string]float64)
	for _, sumKey := range sumKeys {
		// key format: ab:metrics:{experiment}:{group}:sum:{metric}
		parts := strings.Split(sumKey, ":")
		if len(parts) < 6 {
			continue
		}
		metricName := parts[5] // "conversion"
		if metricName == "" {
			continue
		}
		val, err := getRunningAverage(ctx, s.rdb, experiment, group, metricName)
		if err != nil {
			continue
		}
		metrics[metricName] = val
	}
	return metrics, nil
}
