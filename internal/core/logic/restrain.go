package logic

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// RestrainActionAdd bans a user; any other action lifts the ban.
const RestrainActionAdd int32 = 1

// RestrainStore persists restrained users.
type RestrainStore interface {
	AddRestrainUser(ctx context.Context, appID string, uid int64) error
	DelRestrainUser(ctx context.Context, appID string, uid int64) error
}

// RestrainService bans and unbans users. The ban is recorded in MySQL and
// mirrored to a Redis flag checked when issuing login tokens.
type RestrainService struct {
	store RestrainStore
	rdb   redis.UniversalClient
}

// NewRestrainService returns a RestrainService.
func NewRestrainService(st RestrainStore, rdb redis.UniversalClient) *RestrainService {
	return &RestrainService{store: st, rdb: rdb}
}

// Operate bans the user when action is RestrainActionAdd and unbans otherwise.
func (s *RestrainService) Operate(ctx context.Context, appID string, uid int64, action int32) error {
	key := RestrainUserKey(appID, uid)
	if action == RestrainActionAdd {
		if err := s.store.AddRestrainUser(ctx, appID, uid); err != nil {
			return err
		}
		return s.rdb.Set(ctx, key, 1, 0).Err()
	}
	if err := s.rdb.Del(ctx, key).Err(); err != nil {
		return err
	}
	return s.store.DelRestrainUser(ctx, appID, uid)
}
