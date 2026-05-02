package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
	_ "github.com/lib/pq"
)

type Store struct {
	redis    *redis.Client
	postgres *sql.DB
}

func NewStore(redisAddr, redisUsername, redisPassword, postgresURI string, redisDB int) (*Store, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Username: redisUsername,
		Password: redisPassword,
		DB:       redisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	db, err := sql.Open("postgres", postgresURI)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return &Store{redis: rdb, postgres: db}, nil
}

func (s *Store) Close() {
	s.redis.Close()
	s.postgres.Close()
}

func (s *Store) GetItemStats(ctx context.Context, itemID string) (*ItemStats, error) {
	key := fmt.Sprintf("item_success:%s", itemID)
	vals, err := s.redis.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	stats := &ItemStats{}
	if v, ok := vals["like_count"]; ok {
		stats.LikeCount, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := vals["block_count"]; ok {
		stats.BlockCount, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := vals["report_count"]; ok {
		stats.ReportCount, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := vals["view_count"]; ok {
		stats.ViewCount, _ = strconv.ParseInt(v, 10, 64)
	}
	return stats, nil
}

func (s *Store) GetUserAge(ctx context.Context, userID string) (int, error) {
	var labels string
	err := s.postgres.QueryRowContext(ctx,
		"SELECT labels FROM users WHERE user_id = $1", userID).Scan(&labels)
	if err != nil {
		return 0, err
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(labels), &m); err != nil {
		return 0, err
	}
	if v, ok := m["age"].(float64); ok {
		return int(v), nil
	}
	return 0, nil
}

func (s *Store) GetUserQualityFlag(ctx context.Context, userID string) (bool, error) {
	var labels string
	err := s.postgres.QueryRowContext(ctx,
		"SELECT labels FROM users WHERE user_id = $1", userID).Scan(&labels)
	if err != nil {
		return false, err
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(labels), &m); err != nil {
		return false, err
	}
	if v, ok := m["is_high_quality"]; ok {
		if b, ok := v.(bool); ok {
			return b, nil
		}
		if f, ok := v.(float64); ok {
			return f == 1, nil
		}
	}
	return true, nil
}

func (s *Store) GetItemQualityFlag(ctx context.Context, itemID string) (bool, error) {
	var labels string
	err := s.postgres.QueryRowContext(ctx,
		"SELECT labels FROM items WHERE item_id = $1", itemID).Scan(&labels)
	if err != nil {
		return false, err
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(labels), &m); err != nil {
		return false, err
	}
	if v, ok := m["is_high_quality"]; ok {
		if b, ok := v.(bool); ok {
			return b, nil
		}
		if f, ok := v.(float64); ok {
			return f == 1, nil
		}
	}
	return true, nil
}

func (s *Store) ScanKeys(ctx context.Context, pattern string) ([]string, error) {
	var keys []string
	iter := s.redis.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	return keys, iter.Err()
}

func (s *Store) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return s.redis.HGetAll(ctx, key).Result()
}

func (s *Store) HGet(ctx context.Context, key, field string) (string, error) {
	return s.redis.HGet(ctx, key, field).Result()
}

func (s *Store) IncrBy(ctx context.Context, key string, val int64) (int64, error) {
	return s.redis.IncrBy(ctx, key, val).Result()
}
