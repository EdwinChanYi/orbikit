// Package lock implements the Redis based mutexes that serialize chatroom
// operations across service replicas.
package lock

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/yourname/roomkit/internal/chatroom/rediskey"
	"github.com/yourname/roomkit/pkg/log"
)

const (
	lockTTL = 10 * time.Second
	// roomLockAttempts * 10ms is the time AcquireRoomProcess waits.
	roomLockAttempts = 500
	// shortLockAttempts * 50ms is the time the retrying user/room locks wait.
	shortLockAttempts = 40
)

var (
	rdb        redis.UniversalClient
	localLocks sync.Map
)

// Init sets the Redis client used for locking.
func Init(c redis.UniversalClient) { rdb = c }

func setNX(ctx context.Context, key string) (bool, error) {
	ok, err := rdb.SetNX(ctx, key, 1, lockTTL).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	return ok, err
}

func del(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return rdb.Del(ctx, key).Err()
}

func localLock(appID string, roomID int64) *sync.Mutex {
	l, _ := localLocks.LoadOrStore(rediskey.ChatroomProcessLock(appID, roomID), &sync.Mutex{})
	return l.(*sync.Mutex)
}

// AcquireRoomProcess serializes all processing of a room, first within the
// process and then across replicas. It waits up to about five seconds.
func AcquireRoomProcess(ctx context.Context, appID string, roomID int64) bool {
	ll := localLock(appID, roomID)
	ll.Lock()
	key := rediskey.ChatroomProcessLock(appID, roomID)
	for count := 1; ; count++ {
		ok, err := setNX(ctx, key)
		if err != nil {
			log.L().Error("setNX chatroom key error", zap.Error(err))
		}
		if ok {
			return true
		}
		if count > roomLockAttempts {
			ll.Unlock()
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ReleaseRoomProcess releases a lock taken by AcquireRoomProcess.
func ReleaseRoomProcess(appID string, roomID int64) {
	key := rediskey.ChatroomProcessLock(appID, roomID)
	for i := 0; i <= 5; i++ {
		err := del(key)
		if err == nil {
			break
		}
		log.L().Error("del chatroom key error", zap.Error(err))
		time.Sleep(10 * time.Millisecond)
	}
	localLock(appID, roomID).Unlock()
}

// TryRoom makes a single attempt to take the room process lock without the
// in-process mutex. It is used by background jobs.
func TryRoom(ctx context.Context, appID string, roomID int64) bool {
	ok, err := setNX(ctx, rediskey.ChatroomProcessLock(appID, roomID))
	if err != nil {
		log.L().Error("setNX chatroom key error", zap.Error(err))
		return false
	}
	return ok
}

// ReleaseRoom releases a lock taken by TryRoom.
func ReleaseRoom(appID string, roomID int64) bool {
	if err := del(rediskey.ChatroomProcessLock(appID, roomID)); err != nil {
		log.L().Error("del chatroom key error", zap.Error(err))
		return false
	}
	return true
}

// AcquireMic serializes mic extension updates of a room. If the lock cannot
// be taken within about five seconds it is assumed stale and cleared.
func AcquireMic(ctx context.Context, appID string, roomID int64) bool {
	key := rediskey.ChatroomMicLock(appID, roomID)
	for retries := 1; ; retries++ {
		if retries >= roomLockAttempts {
			ReleaseMic(appID, roomID)
			return false
		}
		if ok, err := setNX(ctx, key); err == nil && ok {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ReleaseMic releases a lock taken by AcquireMic.
func ReleaseMic(appID string, roomID int64) {
	key := rediskey.ChatroomMicLock(appID, roomID)
	for retries := 0; retries < 5; retries++ {
		err := del(key)
		if err == nil {
			return
		}
		log.L().Error("ReleaseChatroomMicLock err",
			zap.String("appID", appID), zap.Int64("roomID", roomID), zap.Error(err))
		time.Sleep(10 * time.Millisecond)
	}
}

func acquireShort(ctx context.Context, key string, retry bool) bool {
	for count := 0; ; {
		ok, err := setNX(ctx, key)
		if err != nil {
			log.L().Error("SetNX lock key error", zap.String("key", key), zap.Error(err))
			return false
		}
		if ok {
			return true
		}
		if !retry {
			return false
		}
		count++
		if count >= shortLockAttempts {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func releaseShort(key string) {
	if err := del(key); err != nil {
		log.L().Error("release lock fail", zap.String("key", key), zap.Error(err))
	}
}

// AcquireUser locks a member of a room. With retry it waits up to two seconds.
func AcquireUser(ctx context.Context, appID string, roomID, uid int64, retry bool) bool {
	return acquireShort(ctx, rediskey.UserLock(appID, roomID, uid), retry)
}

// ReleaseUser releases a lock taken by AcquireUser.
func ReleaseUser(appID string, roomID, uid int64) {
	releaseShort(rediskey.UserLock(appID, roomID, uid))
}

// AcquireRoomInfo locks the room info. With retry it waits up to two seconds.
func AcquireRoomInfo(ctx context.Context, appID string, roomID int64, retry bool) bool {
	return acquireShort(ctx, rediskey.ChatroomInfoLock(appID, roomID), retry)
}

// ReleaseRoomInfo releases a lock taken by AcquireRoomInfo.
func ReleaseRoomInfo(appID string, roomID int64) {
	releaseShort(rediskey.ChatroomInfoLock(appID, roomID))
}
