package logic

import (
	"context"
	"strconv"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

func onlineUserCount(ctx context.Context, appID string, roomID int64) (int64, error) {
	v, err := rdb.Get(ctx, rediskey.ChatroomOnlineCount(appID, roomID)).Result()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

func (c *ChatroomContainer) onlineUserCountNoError() int64 {
	n, err := onlineUserCount(c.ctx, c.appId, c.roomId)
	if err != nil {
		if !isNil(err) {
			c.ctx.Error("chatroom get user online counter error", zap.Error(err))
		}
		return 0
	}
	return n
}

func (c *ChatroomContainer) incrOnlineCount(delta int64) {
	if err := rdb.IncrBy(c.ctx, rediskey.ChatroomOnlineCount(c.appId, c.roomId), delta).Err(); err != nil {
		c.ctx.Error("chatroom modify online counter error", zap.Int64("delta", delta), zap.Error(err))
	}
}

func (c *ChatroomContainer) delOnlineCount() {
	if err := rdb.Del(c.ctx, rediskey.ChatroomOnlineCount(c.appId, c.roomId)).Err(); err != nil {
		c.ctx.Error("chatroom close del online counter error", zap.Error(err))
	}
}

func (c *ChatroomContainer) incrRobotCount(delta int64) {
	if err := rdb.IncrBy(c.ctx, rediskey.ChatroomRobotCount(c.appId, c.roomId), delta).Err(); err != nil {
		c.ctx.Error("chatroom modify robot counter error", zap.Int64("delta", delta), zap.Error(err))
	}
}

func (c *ChatroomContainer) delRobotCount() {
	if err := rdb.Del(c.ctx, rediskey.ChatroomRobotCount(c.appId, c.roomId)).Err(); err != nil {
		c.ctx.Error("chatroom robot count del counter error", zap.Error(err))
	}
}

func (c *ChatroomContainer) robotCountNoError() int64 {
	return robotCountNoError(c.ctx, c.appId, c.roomId)
}

func robotCountNoError(ctx context.Context, appID string, roomID int64) int64 {
	v, err := rdb.Get(ctx, rediskey.ChatroomRobotCount(appID, roomID)).Result()
	if err != nil {
		if !isNil(err) {
			log.L().Error("chatroom get robot counter error", zap.Error(err))
		}
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func usedMicCount(ctx context.Context, appID string, roomID int64) (int64, error) {
	n, err := rdb.Get(ctx, rediskey.ChatroomUsedMicSpaceCount(appID, roomID)).Int64()
	if isNil(err) {
		return 0, nil
	}
	return n, err
}
