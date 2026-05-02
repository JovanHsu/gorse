package main

import (
	"context"
	"fmt"
	"hash/crc32"
)

func AssignGroup(userID, experiment string) string {
	bucket := crc32.ChecksumIEEE([]byte(userID + experiment)) % 100
	switch {
	case bucket < 10:
		return "A"
	case bucket < 20:
		return "B"
	default:
		return "control"
	}
}

func (s *Server) storeAssign(ctx context.Context, experiment, userID, group string) error {
	key := fmt.Sprintf("ab:%s:%s", experiment, userID)
	return s.rdb.Set(ctx, key, group, 0).Err()
}
