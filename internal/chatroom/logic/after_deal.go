package logic

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/internal/chatroom/lock"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

const afterDealBatch = 10

// AddTaskOfDealChatroom queues a room for a rank refresh.
func AddTaskOfDealChatroom(ctx context.Context, appID string, roomID int64) {
	rdb.SAdd(ctx, rediskey.ChatroomAfterDealQueue, rediskey.QueueMember(appID, roomID))
}

// QueueDeal refreshes the rank entries of up to ten queued rooms.
func QueueDeal(ctx context.Context) {
	for i := 0; i < afterDealBatch; i++ {
		n, err := rdb.Exists(ctx, rediskey.ChatroomAfterDealQueue).Result()
		if err != nil {
			log.L().Error("check key is exist fail", zap.Error(err))
		} else if n <= 0 {
			return
		}

		val, err := rdb.SPop(ctx, rediskey.ChatroomAfterDealQueue).Result()
		if err != nil && !isNil(err) {
			log.L().Error("EnterRoomAfterDeal fail", zap.Error(err))
			continue
		}
		if val == "" {
			return
		}
		appID, roomID, ok := rediskey.ParseQueueMember(val)
		if !ok {
			log.L().Error("invalid after deal queue member", zap.String("val", val))
			continue
		}
		if !lock.TryRoom(ctx, appID, roomID) {
			AddTaskOfDealChatroom(ctx, appID, roomID)
			continue
		}
		go DealChatroomInfo(appID, roomID)
	}
}

// AddUserToQueue schedules uid to leave all rooms after the offline grace period.
func AddUserToQueue(ctx context.Context, appID string, uid int64) error {
	return rdb.ZAdd(ctx, rediskey.UserAfterDealQueue, redis.Z{
		Score:  float64(time.Now().UnixNano()),
		Member: rediskey.QueueMember(appID, uid),
	}).Err()
}

// RemoveUserFromQueue cancels a pending offline leave.
func RemoveUserFromQueue(ctx context.Context, appID string, uid int64) error {
	return rdb.ZRem(ctx, rediskey.UserAfterDealQueue, rediskey.QueueMember(appID, uid)).Err()
}

// DealUserQueue removes users whose offline grace period has elapsed from all rooms.
func DealUserQueue(ctx context.Context) {
	deadline := time.Now().Add(-settings.OfflineLeaveAfter).UnixNano()
	users, err := rdb.ZRangeByScore(ctx, rediskey.UserAfterDealQueue, &redis.ZRangeBy{
		Min:   "-inf",
		Max:   strconv.FormatInt(deadline, 10),
		Count: afterDealBatch,
	}).Result()
	if err != nil {
		if !isNil(err) {
			log.L().Error("DealUserQueue ZRangeByScore fail", zap.Error(err))
		}
		return
	}

	for _, user := range users {
		log.L().Info("DealUserQueue", zap.String("user", user))
		rdb.ZRem(ctx, rediskey.UserAfterDealQueue, user)

		go func(u string) {
			appID, uid, ok := rediskey.ParseQueueMember(u)
			if !ok {
				return
			}
			c, cancel := bg()
			defer cancel()
			rdb.Del(c, rediskey.UserOfflineChatroom(appID, uid))
			BatchOutChatroom(appID, uid)
		}(user)
	}
}

// DealChatroomInfo recomputes the rank scores of a room. The caller must
// hold the room lock taken by lock.TryRoom; it is released here.
func DealChatroomInfo(appID string, roomID int64) {
	defer lock.ReleaseRoom(appID, roomID)
	ctx, cancel := bg()
	defer cancel()

	n, err := rdb.Exists(ctx, rediskey.ChatroomInfo(appID, roomID)).Result()
	if err != nil {
		log.L().Error("exists fail", zap.Error(err))
		AddTaskOfDealChatroom(ctx, appID, roomID)
		return
	}
	if n <= 0 {
		_ = delMemmicZ(ctx, appID, roomID)
		_ = delMicmemZ(ctx, appID, roomID)
		return
	}

	onlineCnt, _ := onlineUserCount(ctx, appID, roomID)
	micCnt, err := usedMicCount(ctx, appID, roomID)
	if err != nil {
		log.L().Error("get used mic count fail", zap.Error(err))
		AddTaskOfDealChatroom(ctx, appID, roomID)
		return
	}

	if err := rdb.ZAdd(ctx, rediskey.ChatroomOnlineMemberRank(appID),
		redis.Z{Score: memicScore(onlineCnt, micCnt), Member: roomID}).Err(); err != nil {
		log.L().Error("zadd fail", zap.String("key", rediskey.ChatroomOnlineMemberRank(appID)), zap.Error(err))
	}
	if err := rdb.ZAdd(ctx, rediskey.ChatroomUsedMicSpaceRank(appID),
		redis.Z{Score: micmemScore(onlineCnt, micCnt), Member: roomID}).Err(); err != nil {
		log.L().Error("zadd fail", zap.String("key", rediskey.ChatroomUsedMicSpaceRank(appID)), zap.Error(err))
	}
	log.L().Info("afterDeal", zap.String("appID", appID), zap.Int64("roomID", roomID))
}
