package main

import (
	"context"
	"fmt"

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
	sumKeys, err := s.rdb.Keys(ctx, fmt.Sprintf("ab:metrics:%s:%s:sum:*", experiment, group)).Result()
	if err != nil {
		return nil, err
	}

	metrics := make(map[string]float64)
	for _, sumKey := range sumKeys {
		metric := ""
		fmt.Sscanf(sumKey, "ab:metrics:%s:%s:sum:%s", &metric, &metric, &metric)
		if metric == "" {
			continue
		}
		val, err := getRunningAverage(ctx, s.rdb, experiment, group, metric)
		if err != nil {
			continue
		}
		metrics[metric] = val
	}
	return metrics, nil
}
