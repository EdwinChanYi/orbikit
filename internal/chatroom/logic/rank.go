package logic

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

const (
	micPosition float64 = 100
	memPosition float64 = 100000000

	defaultRankPageSize = 20
)

// memicScore orders rooms by online members first, then by used mics.
func memicScore(memCnt, micCnt int64) float64 {
	return float64(memCnt)*micPosition + float64(micCnt)
}

// micmemScore orders rooms by used mics first, then by online members.
func micmemScore(memCnt, micCnt int64) float64 {
	return float64(micCnt)*memPosition + float64(memCnt)
}

// rankWindow converts a 1-based page into a ZRANGEBYSCORE offset and count.
func rankWindow(page, pageSize int32) (offset, count int64) {
	count = defaultRankPageSize
	if pageSize != 0 {
		count = int64(pageSize)
	}
	return count * (int64(page) - 1), count
}

func roomCounts(ctx context.Context, appID string, roomID int64) (memCnt, micCnt int64, err error) {
	if memCnt, err = onlineUserCount(ctx, appID, roomID); err != nil {
		if !isNil(err) {
			return 0, 0, err
		}
		memCnt = 0
	}
	if micCnt, err = usedMicCount(ctx, appID, roomID); err != nil {
		return 0, 0, err
	}
	return memCnt, micCnt, nil
}

func updateMemmicZ(ctx context.Context, appID string, roomID int64) error {
	memCnt, micCnt, err := roomCounts(ctx, appID, roomID)
	if err == nil {
		err = rdb.ZAdd(ctx, rediskey.ChatroomOnlineMemberRank(appID),
			redis.Z{Score: memicScore(memCnt, micCnt), Member: roomID}).Err()
	}
	if err != nil {
		log.L().Error("update memmic Z failed", zap.Error(err), zap.String("appId", appID), zap.Int64("roomId", roomID))
	}
	return err
}

func updateMicmemZ(ctx context.Context, appID string, roomID int64) error {
	memCnt, micCnt, err := roomCounts(ctx, appID, roomID)
	if err == nil {
		err = rdb.ZAdd(ctx, rediskey.ChatroomUsedMicSpaceRank(appID),
			redis.Z{Score: micmemScore(memCnt, micCnt), Member: roomID}).Err()
	}
	if err != nil {
		log.L().Error("update micmem Z failed", zap.Error(err), zap.String("appId", appID), zap.Int64("roomId", roomID))
	}
	return err
}

func delMemmicZ(ctx context.Context, appID string, roomID int64) error {
	return rdb.ZRem(ctx, rediskey.ChatroomOnlineMemberRank(appID), roomID).Err()
}

func delMicmemZ(ctx context.Context, appID string, roomID int64) error {
	return rdb.ZRem(ctx, rediskey.ChatroomUsedMicSpaceRank(appID), roomID).Err()
}

func rankRoomIDs(ctx context.Context, key string, isDesc bool, page, pageSize int32) ([]string, error) {
	offset, count := rankWindow(page, pageSize)
	opt := &redis.ZRangeBy{Min: "-inf", Max: "+inf", Offset: offset, Count: count}
	if isDesc {
		return rdb.ZRevRangeByScore(ctx, key, opt).Result()
	}
	return rdb.ZRangeByScore(ctx, key, opt).Result()
}

// GetSortRoomByMembers pages rooms ordered by online member count.
func GetSortRoomByMembers(ctx context.Context, appID string, isDesc bool, page, pageSize int32) (
	servicepb.ErrorCode, []*servicepb.ChatroomMemberCountInfo, error) {
	roomIDs, err := rankRoomIDs(ctx, rediskey.ChatroomOnlineMemberRank(appID), isDesc, page, pageSize)
	if err != nil {
		log.L().Error("GetSortRoomByMembers ZRangeByScore/ZRevRangeByScore failed",
			zap.String("appID", appID), zap.Bool("isDesc", isDesc), zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil, err
	}

	var infos []*servicepb.ChatroomMemberCountInfo
	for _, rID := range roomIDs {
		roomID, err := strconv.ParseInt(rID, 10, 64)
		if err != nil {
			log.L().Error("GetSortRoomByMembers parse room id err",
				zap.String("appID", appID), zap.String("roomID", rID), zap.Error(err))
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, infos, err
		}
		memCnt, err := onlineUserCount(ctx, appID, roomID)
		if err != nil {
			if !isNil(err) {
				log.L().Error("GetSortRoomByMembers onlineUserCount failed",
					zap.String("appID", appID), zap.Int64("roomID", roomID), zap.Error(err))
				return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, infos, err
			}
			memCnt = 0
		}
		infos = append(infos, &servicepb.ChatroomMemberCountInfo{
			ChatroomId:  roomID,
			OnlineCount: int32(memCnt),
			RobotCount:  int32(robotCountNoError(ctx, appID, roomID)),
		})
	}
	return servicepb.ErrorCode_SUCCESS, infos, nil
}

// GetSortRoomByUsedMicCount pages rooms ordered by used mic count. The
// returned OnlineCount carries the used mic count.
func GetSortRoomByUsedMicCount(ctx context.Context, appID string, isDesc bool, page, pageSize int32) (
	servicepb.ErrorCode, []*servicepb.ChatroomMemberCountInfo, error) {
	roomIDs, err := rankRoomIDs(ctx, rediskey.ChatroomUsedMicSpaceRank(appID), isDesc, page, pageSize)
	if err != nil {
		log.L().Error("GetSortRoomByUsedMicCount ZRangeByScore/ZRevRangeByScore failed",
			zap.String("appID", appID), zap.Bool("isDesc", isDesc), zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil, err
	}

	var infos []*servicepb.ChatroomMemberCountInfo
	for _, rID := range roomIDs {
		roomID, err := strconv.ParseInt(rID, 10, 64)
		if err != nil {
			log.L().Error("GetSortRoomByUsedMicCount parse room id err",
				zap.String("appID", appID), zap.String("roomID", rID), zap.Error(err))
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, infos, err
		}
		micCnt, err := usedMicCount(ctx, appID, roomID)
		if err != nil {
			log.L().Error("GetSortRoomByUsedMicCount usedMicCount failed",
				zap.String("appID", appID), zap.Int64("roomID", roomID), zap.Error(err))
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, infos, err
		}
		infos = append(infos, &servicepb.ChatroomMemberCountInfo{
			ChatroomId:  roomID,
			OnlineCount: int32(micCnt),
		})
	}
	return servicepb.ErrorCode_SUCCESS, infos, nil
}
