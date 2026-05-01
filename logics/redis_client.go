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
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logics

import (
	"context"
	"fmt"
	"time"

	"github.com/juju/errors"
	"github.com/redis/go-redis/v9"
)

// RedisClient is a thin wrapper around go-redis client for lifecycle management.
// It handles exposure counting, fatigue state, and lifecycle classification caching.
type RedisClient struct {
	client *redis.Client
}

// NewRedisClient creates a new Redis client from address and password.
func NewRedisClient(addr, password string) (*RedisClient, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           0,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, errors.Trace(err)
	}
	return &RedisClient{client: client}, nil
}

// NewRedisClientFromURL creates a Redis client from a URL (e.g., redis://addr/db).
func NewRedisClientFromURL(url string) (*RedisClient, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, errors.Trace(err)
	}
	opt.Protocol = 2
	client := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, errors.Trace(err)
	}
	return &RedisClient{client: client}, nil
}

// Close closes the Redis connection.
func (r *RedisClient) Close() error {
	return r.client.Close()
}

// Key patterns:
//   - lifecycle:{userId}         → JSON LifecycleProfile (TTL: 5min)
//   - fatigue:{userId}            → Hash: swipe_count, last_match_at, recent_types (TTL: 24h)
//   - exposure:{gender}:{userId}:{date} → integer counter (TTL: 48h)

const (
	exposureKeyTTL = 48 * time.Hour
	fatigueKeyTTL  = 24 * time.Hour
)

// ---- Exposure Counting ----

// IncExposure increments the exposure count for a user and returns the new count.
func (r *RedisClient) IncExposure(ctx context.Context, gender, userId string) (int64, error) {
	if r == nil || r.client == nil {
		return 0, nil
	}
	date := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("exposure:%s:%s:%s", gender, userId, date)
	count, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, errors.Trace(err)
	}
	// Set TTL on first increment (within the same day)
	if count == 1 {
		r.client.Expire(ctx, key, exposureKeyTTL)
	}
	return count, nil
}

// GetExposureCount returns the current day's exposure count for a user.
func (r *RedisClient) GetExposureCount(ctx context.Context, gender, userId string) (int64, error) {
	if r == nil || r.client == nil {
		return 0, nil
	}
	date := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("exposure:%s:%s:%s", gender, userId, date)
	count, err := r.client.Get(ctx, key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return count, errors.Trace(err)
}

// GetExposureCounts returns exposure counts for multiple users.
func (r *RedisClient) GetExposureCounts(ctx context.Context, gender string, userIds []string) (map[string]int64, error) {
	if r == nil || r.client == nil {
		return nil, nil
	}
	date := time.Now().Format("2006-01-02")
	pipe := r.client.Pipeline()
	cmds := make(map[string]*redis.StringCmd)
	for _, userId := range userIds {
		key := fmt.Sprintf("exposure:%s:%s:%s", gender, userId, date)
		cmds[userId] = pipe.Get(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, errors.Trace(err)
	}
	counts := make(map[string]int64)
	for userId, cmd := range cmds {
		count, err := cmd.Int64()
		if err == nil {
			counts[userId] = count
		}
	}
	return counts, nil
}

// ---- Fatigue State ----

// fatigueKey returns the Redis key for fatigue state.
func fatigueKey(userId string) string {
	return fmt.Sprintf("fatigue:%s", userId)
}

// IncSwipeCount increments the consecutive swipe count for a user.
func (r *RedisClient) IncSwipeCount(ctx context.Context, userId string) error {
	if r == nil || r.client == nil {
		return nil
	}
	key := fatigueKey(userId)
	pipe := r.client.Pipeline()
	pipe.HIncrBy(ctx, key, "swipe_count", 1)
	pipe.Expire(ctx, key, fatigueKeyTTL)
	_, err := pipe.Exec(ctx)
	return errors.Trace(err)
}

// ResetSwipeCount resets the consecutive swipe count after a match.
func (r *RedisClient) ResetSwipeCount(ctx context.Context, userId string) error {
	if r == nil || r.client == nil {
		return nil
	}
	key := fatigueKey(userId)
	pipe := r.client.Pipeline()
	pipe.HSet(ctx, key, "swipe_count", "0")
	pipe.HSet(ctx, key, "last_match_at", time.Now().Format(time.RFC3339))
	pipe.HSet(ctx, key, "recent_types", "[]")
	pipe.Expire(ctx, key, fatigueKeyTTL)
	_, err := pipe.Exec(ctx)
	return errors.Trace(err)
}

// GetFatigueState returns the current fatigue state for a user.
func (r *RedisClient) GetFatigueState(ctx context.Context, userId string) (swipeCount int64, lastMatchAt time.Time, err error) {
	if r == nil || r.client == nil {
		return 0, time.Time{}, nil
	}
	key := fatigueKey(userId)
	val, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return 0, time.Time{}, errors.Trace(err)
	}
	if count, ok := val["swipe_count"]; ok {
		fmt.Sscanf(count, "%d", &swipeCount)
	}
	if lastMatch, ok := val["last_match_at"]; ok && lastMatch != "" {
		lastMatchAt, _ = time.Parse(time.RFC3339, lastMatch)
	}
	return swipeCount, lastMatchAt, nil
}

// ---- Lifecycle Cache ----

// lifecycleKey returns the Redis key for lifecycle profile cache.
func lifecycleKey(userId string) string {
	return fmt.Sprintf("lifecycle:%s", userId)
}

// ---- Client Access ----

// Client returns the underlying redis.Client for advanced usage.
func (r *RedisClient) Client() *redis.Client {
	return r.client
}
