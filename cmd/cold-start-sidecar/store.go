package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type Store struct {
	redis        *redis.Client
	dataStoreURI string
	tablePrefix  string
}

func NewStore(redisAddr, redisUsername, redisPassword, dataStoreURI, tablePrefix string, redisDB int) (*Store, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Username: redisUsername,
		Password: redisPassword,
		DB:       redisDB,
	})
	return &Store{
		redis:        rdb,
		dataStoreURI: dataStoreURI,
		tablePrefix:  tablePrefix,
	}, nil
}

func (s *Store) Close() error {
	return s.redis.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.redis.Ping(ctx).Err()
}

// GetUserSignupTime returns the signup time for a user from Redis.
func (s *Store) GetUserSignupTime(ctx context.Context, userID string) (time.Time, error) {
	key := fmt.Sprintf("user:%s:signup_time", userID)
	val, err := s.redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// IsNewUser returns true if user registered within the last 7 days.
func (s *Store) IsNewUser(ctx context.Context, userID string) (bool, error) {
	signup, err := s.GetUserSignupTime(ctx, userID)
	if err != nil {
		return false, err
	}
	if signup.IsZero() {
		return false, nil
	}
	return time.Since(signup) < 7*24*time.Hour, nil
}

// GetExposureCount returns the number of times an item has been exposed
// in the last 7 days for a given gender group.
func (s *Store) GetExposureCount(ctx context.Context, itemID, gender string) (int64, error) {
	now := time.Now()
	var total int64
	for i := 0; i < 7; i++ {
		date := now.AddDate(0, 0, -i).Format("20060102")
		key := fmt.Sprintf("sd:exposure:%s:%s", gender, date)
		val, err := s.redis.Get(ctx, key).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return 0, err
		}
		count, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return 0, err
		}
		// Check if item is in the set
		memKey := fmt.Sprintf("sd:exposure:%s:%s:members", gender, date)
		isMember, err := s.redis.SIsMember(ctx, memKey, itemID).Result()
		if err != nil && err != redis.Nil {
			return 0, err
		}
		if isMember {
			total += count
		}
	}
	return total, nil
}

// GetHighExposureItems returns items exposed more than maxExposures times
// in the last 7 days for a given gender.
func (s *Store) GetHighExposureItems(ctx context.Context, gender string, maxExposures int64) ([]string, error) {
	now := time.Now()
	highExp := make(map[string]int64)
	for i := 0; i < 7; i++ {
		date := now.AddDate(0, 0, -i).Format("20060102")
		memKey := fmt.Sprintf("sd:exposure:%s:%s:members", gender, date)
		items, err := s.redis.SMembers(ctx, memKey).Result()
		if err == redis.Nil || len(items) == 0 {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, itemID := range items {
			key := fmt.Sprintf("sd:exposure:%s:%s", gender, date)
			val, err := s.redis.HGet(ctx, key, itemID).Result()
			if err == redis.Nil {
				continue
			}
			if err != nil {
				return nil, err
			}
			count, _ := strconv.ParseInt(val, 10, 64)
			highExp[itemID] += count
		}
	}
	var result []string
	for itemID, count := range highExp {
		if count > maxExposures {
			result = append(result, itemID)
		}
	}
	return result, nil
}
