package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

const (
	authLockTTL      = 5 * time.Second
	authLockRetry    = 50 * time.Millisecond
	userInfoTTL      = 10 * time.Minute
	kickPollAttempts = 40
	kickPollInterval = 50 * time.Millisecond
)

// Store wraps the gateway's Redis operations.
type Store struct {
	rdb redis.UniversalClient
}

// New returns a Store backed by rdb.
func New(rdb redis.UniversalClient) *Store { return &Store{rdb: rdb} }

// Redis exposes the underlying client.
func (s *Store) Redis() redis.UniversalClient { return s.rdb }

// LockUser spins until it owns the per-user auth lock or ctx is done.
func (s *Store) LockUser(ctx context.Context, appID string, uid int64) error {
	key := AuthLockKey(appID, uid)
	for {
		ok, err := s.rdb.SetNX(ctx, key, 1, authLockTTL).Result()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(authLockRetry):
		}
	}
}

// UnlockUser releases the per-user auth lock.
func (s *Store) UnlockUser(appID string, uid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.rdb.Del(ctx, AuthLockKey(appID, uid)).Err(); err != nil {
		log.L().Warn("release auth lock", zap.String("app_id", appID), zap.Int64("uid", uid), zap.Error(err))
	}
}

// LastLoginToken returns the token of the user's previous AUTH, or "".
func (s *Store) LastLoginToken(ctx context.Context, appID string, uid int64) (string, error) {
	v, err := s.rdb.Get(ctx, LastLoginTokenKey(appID, uid)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

// SetLastLoginToken records the token of the current AUTH.
func (s *Store) SetLastLoginToken(ctx context.Context, appID string, uid int64, token string) error {
	return s.rdb.Set(ctx, LastLoginTokenKey(appID, uid), token, 0).Err()
}

// KickOtherConnections asks every gateway to drop the user's existing
// connection and waits, bounded, until the link status key disappears.
func (s *Store) KickOtherConnections(ctx context.Context, machineID uint16, connID uint64, appID string, uid int64) error {
	statusKey := LinkStatusKey(appID, uid)
	event := NewConnectionEvent(machineID, connID, appID, uid)
	for i := 0; i < kickPollAttempts; i++ {
		n, err := s.rdb.Exists(ctx, statusKey).Result()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if err := s.rdb.Publish(ctx, NewConnectionChannel, event).Err(); err != nil {
			log.L().Error("publish new connection event", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(kickPollInterval):
		}
	}
	return nil
}

// SetLinkStatus marks the user as connected.
func (s *Store) SetLinkStatus(ctx context.Context, appID string, uid int64) error {
	return s.rdb.Set(ctx, LinkStatusKey(appID, uid), 1, 0).Err()
}

// ClearLinkStatus marks the user as disconnected.
func (s *Store) ClearLinkStatus(ctx context.Context, appID string, uid int64) error {
	return s.rdb.Del(ctx, LinkStatusKey(appID, uid)).Err()
}

// UserInfo is the short-link view of an authenticated connection.
type UserInfo struct {
	AppID      string
	UID        int64
	DeviceID   string
	MaskKey    string
	LoginToken string
}

// SaveUserInfo stores the short-link credentials under authToken.
func (s *Store) SaveUserInfo(ctx context.Context, authToken string, u *UserInfo) error {
	key := UserInfoKey(authToken)
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, key, map[string]any{
			"app_id":      u.AppID,
			"uid":         u.UID,
			"device_id":   u.DeviceID,
			"mask_key":    u.MaskKey,
			"login_token": u.LoginToken,
		})
		p.Expire(ctx, key, userInfoTTL)
		return nil
	})
	return err
}

// TouchUserInfo extends the lifetime of the short-link credentials.
func (s *Store) TouchUserInfo(ctx context.Context, authToken string) error {
	return s.rdb.Expire(ctx, UserInfoKey(authToken), userInfoTTL).Err()
}

// ErrUnknownToken means the auth token has no stored credentials.
var ErrUnknownToken = errors.New("store: unknown auth token")

// LoadUserInfo returns the credentials stored under authToken.
func (s *Store) LoadUserInfo(ctx context.Context, authToken string) (*UserInfo, error) {
	m, err := s.rdb.HGetAll(ctx, UserInfoKey(authToken)).Result()
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, ErrUnknownToken
	}
	uid, err := strconv.ParseInt(m["uid"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("store: bad uid in user info: %w", err)
	}
	return &UserInfo{
		AppID:      m["app_id"],
		UID:        uid,
		DeviceID:   m["device_id"],
		MaskKey:    m["mask_key"],
		LoginToken: m["login_token"],
	}, nil
}

// RecordLogin updates the daily-active and online sets. Failures are logged
// because statistics must never block a login.
func (s *Store) RecordLogin(ctx context.Context, appID string, uid int64, deviceID string) {
	now := time.Now()
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.SAdd(ctx, DailyActiveAppIDsKey(now.Format(dateLayout)), appID)
		p.SAdd(ctx, DailyActiveUIDsKey(appID, now), uid)
		p.SAdd(ctx, OnlineUIDsKey(appID), uid)
		if deviceID != "" {
			p.SAdd(ctx, DailyActiveDevicesKey(appID, now), deviceID)
			p.SAdd(ctx, OnlineDevicesKey(appID), deviceID)
		}
		return nil
	})
	if err != nil {
		log.L().Warn("record login stats", zap.String("app_id", appID), zap.Int64("uid", uid), zap.Error(err))
	}
}

// RecordLogout removes the user from the online sets.
func (s *Store) RecordLogout(ctx context.Context, appID string, uid int64, deviceID string) {
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.SRem(ctx, OnlineUIDsKey(appID), uid)
		if deviceID != "" {
			p.SRem(ctx, OnlineDevicesKey(appID), deviceID)
		}
		return nil
	})
	if err != nil {
		log.L().Warn("record logout stats", zap.String("app_id", appID), zap.Int64("uid", uid), zap.Error(err))
	}
}

// IncrDailyMsg counts a chatroom message; moderated also counts it as checked.
func (s *Store) IncrDailyMsg(ctx context.Context, appID string, moderated bool) {
	now := time.Now()
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.IncrBy(ctx, DailyTextMsgKey(appID, now), 1)
		if moderated {
			p.IncrBy(ctx, DailyModeratedMsgKey(appID, now), 1)
		}
		return nil
	})
	if err != nil {
		log.L().Warn("count daily message", zap.String("app_id", appID), zap.Error(err))
	}
}

// RollDailyActive seeds today's daily-active sets with users who stayed
// online across midnight. Only one gateway does the work per day.
func (s *Store) RollDailyActive(ctx context.Context, now time.Time) error {
	ok, err := s.rdb.SetNX(ctx, DailyActiveLockKey, 1, time.Second).Result()
	if err != nil || !ok {
		return err
	}
	defer func() {
		if err := s.rdb.Del(context.Background(), DailyActiveLockKey).Err(); err != nil {
			log.L().Warn("release daily active lock", zap.Error(err))
		}
	}()

	yesterday := now.AddDate(0, 0, -1).Format(dateLayout)
	appIDs, err := s.rdb.SMembers(ctx, DailyActiveAppIDsKey(yesterday)).Result()
	if err != nil {
		return err
	}
	today := DailyActiveAppIDsKey(now.Format(dateLayout))
	for _, appID := range appIDs {
		online, err := s.rdb.SCard(ctx, OnlineUIDsKey(appID)).Result()
		if err != nil {
			return err
		}
		if online == 0 {
			continue
		}
		if err := s.rdb.SAdd(ctx, today, appID).Err(); err != nil {
			return err
		}
		uids := DailyActiveUIDsKey(appID, now)
		if err := s.rdb.SUnionStore(ctx, uids, uids, OnlineUIDsKey(appID)).Err(); err != nil {
			return err
		}
		devices := DailyActiveDevicesKey(appID, now)
		if err := s.rdb.SUnionStore(ctx, devices, devices, OnlineDevicesKey(appID)).Err(); err != nil {
			return err
		}
	}
	return nil
}

// RunDailyActiveRoller calls RollDailyActive shortly after every midnight
// until ctx is cancelled.
func (s *Store) RunDailyActiveRoller(ctx context.Context) {
	t := time.NewTicker(50 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if now.Hour() != 0 || now.Minute() != 0 {
				continue
			}
			if err := s.RollDailyActive(ctx, now); err != nil {
				log.L().Error("roll daily active users", zap.Error(err))
			}
		}
	}
}

// ParseNewConnectionEvent decodes a NewConnectionChannel payload.
func ParseNewConnectionEvent(payload string) (machineID uint16, connID uint64, appID string, uid int64, err error) {
	const prefix = NewConnectionChannel + ":"
	if !strings.HasPrefix(payload, prefix) {
		return 0, 0, "", 0, fmt.Errorf("store: bad event %q", payload)
	}
	rest := payload[len(prefix):]
	// machine:conn:app:uid, where the app id itself may not contain ':'.
	parts := strings.Split(rest, ":")
	if len(parts) != 4 {
		return 0, 0, "", 0, fmt.Errorf("store: bad event %q", payload)
	}
	m, err1 := strconv.ParseUint(parts[0], 10, 16)
	c, err2 := strconv.ParseUint(parts[1], 10, 64)
	u, err3 := strconv.ParseInt(parts[3], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return 0, 0, "", 0, fmt.Errorf("store: bad event %q: %w", payload, err)
	}
	return uint16(m), c, parts[2], u, nil
}
