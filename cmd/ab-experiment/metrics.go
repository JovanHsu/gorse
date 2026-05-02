package main

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func (s *Server) recordMetric(ctx context.Context, experiment, userID, metric string, value float64, group string) error {
	sumKey := fmt.Sprintf("ab:metrics:%s:%s:sum:%s", experiment, group, metric)
	countKey := fmt.Sprintf("ab:metrics:%s:%s:count:%s", experiment, group, metric)
	pipe := s.rdb.Pipeline()
	pipe.HIncrBy(ctx, sumKey, metric, int64(value*1000000))
	pipe.HIncrBy(ctx, countKey, metric, 1)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Server) recordUser(ctx context.Context, experiment, userID, group string) error {
	key := fmt.Sprintf("ab:metrics:%s:%s:users", experiment, group)
	return s.rdb.SAdd(ctx, key, userID).Err()
}

func getRunningAverage(ctx context.Context, rdb *redis.Client, experiment, group, metric string) (float64, error) {
	sumKey := fmt.Sprintf("ab:metrics:%s:%s:sum:%s", experiment, group, metric)
	countKey := fmt.Sprintf("ab:metrics:%s:%s:count:%s", experiment, group, metric)

	sumStr, err := rdb.HGet(ctx, sumKey, metric).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	countStr, err := rdb.HGet(ctx, countKey, metric).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var sum, count int64
	fmt.Sscanf(sumStr, "%d", &sum)
	fmt.Sscanf(countStr, "%d", &count)

	if count == 0 {
		return 0, nil
	}
	return float64(sum) / float64(count) / 1000000, nil
}

func getSampleSize(ctx context.Context, rdb *redis.Client, experiment, group string) (int64, error) {
	key := fmt.Sprintf("ab:metrics:%s:%s:users", experiment, group)
	return rdb.SCard(ctx, key).Result()
}
