package logic

import (
	"context"
	"encoding/json"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/model"
	"github.com/yourname/roomkit/internal/chatroom/rediskey"
	"github.com/yourname/roomkit/internal/chatroom/reqctx"
)

// ChatroomContainer binds a room to the request context.
type ChatroomContainer struct {
	appId    string
	roomId   int64
	ctx      *reqctx.Context
	isExists bool
}

var validApps sync.Map

// IsAppValid asks the core service whether appID may use the chatroom
// service. Valid apps are cached for the lifetime of the process.
func IsAppValid(ctx *reqctx.Context, appID string) (bool, error) {
	if _, ok := validApps.Load(appID); ok {
		return true, nil
	}
	cctx, cancel := context.WithTimeout(ctx, coreCallTimeout)
	defer cancel()
	reply, err := core.IsAppValid(cctx, &servicepb.IsAppValidRequest{AppId: appID})
	if err != nil {
		return false, err
	}
	if reply.IsValid {
		validApps.Store(appID, struct{}{})
	}
	return reply.IsValid, nil
}

// GetChatroom loads the existence state of a room. Redis is authoritative;
// MySQL is only consulted when Redis is unavailable.
func GetChatroom(ctx *reqctx.Context, appID string, roomID int64) (*ChatroomContainer, error) {
	key := rediskey.ChatroomInfo(appID, roomID)
	n, err := rdb.Exists(ctx, key).Result()
	if err != nil {
		var info model.ChatroomInfo
		has, dbErr := first(db.Where("app_id = ? AND chatroom_id = ?", appID, roomID), &info)
		if dbErr != nil {
			return nil, dbErr
		}
		n = 0
		if has {
			n = 1
		}
	}
	return &ChatroomContainer{appId: appID, roomId: roomID, ctx: ctx, isExists: n > 0}, nil
}

// BaseCheck verifies the room exists and is open.
func (c *ChatroomContainer) BaseCheck() (servicepb.ErrorCode, error) {
	if !c.isExists {
		return servicepb.ErrorCode_CHATROOM_NOT_EXISTS, nil
	}
	opened, err := c.IsOpened()
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if !opened {
		return servicepb.ErrorCode_CHATROOM_CLOSED, nil
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// GetChatroomInfoToMap returns the room info as broadcast event content.
func (c *ChatroomContainer) GetChatroomInfoToMap() map[string][]byte {
	errCode, info, err := c.GetChatroomInfo()
	if errCode != servicepb.ErrorCode_SUCCESS || err != nil {
		return nil
	}
	content, err := proto.Marshal(info)
	if err != nil {
		return nil
	}
	return map[string][]byte{"data": content}
}

// GetChatroomInfo reads the room info from the cache, falling back to MySQL.
func (c *ChatroomContainer) GetChatroomInfo() (servicepb.ErrorCode, *servicepb.ChatroomInfo, error) {
	var info model.ChatroomInfo
	data, err := rdb.Get(c.ctx, rediskey.ChatroomInfo(c.appId, c.roomId)).Result()
	if err == nil {
		if err := json.Unmarshal([]byte(data), &info); err != nil {
			return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, nil, err
		}
	} else {
		cacheMiss := isNil(err)
		has, err := first(db.Where("app_id = ? AND chatroom_id = ?", c.appId, c.roomId), &info)
		if err != nil {
			c.ctx.Error("GetChatroomInfo db get err", zap.Error(err))
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil, err
		}
		if has && cacheMiss {
			if err := syncChatroomInfoToRedis(c.ctx, c.appId, c.roomId, &info); err != nil {
				c.ctx.Error("GetChatroomInfo syncChatroomInfoToRedis err", zap.Error(err))
				return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil, err
			}
		}
	}

	return servicepb.ErrorCode_SUCCESS, &servicepb.ChatroomInfo{
		ChatroomId:          c.roomId,
		Name:                info.Name,
		Announcement:        info.Announcement,
		OwnerUid:            info.OwnerUid,
		Ext:                 info.Ext,
		OnlineCount:         c.onlineUserCountNoError(),
		IsMuteAll:           info.IsMuted,
		CustomInfoKeyFilter: info.Filter,
		RobotCount:          c.robotCountNoError(),
	}, nil
}

func syncChatroomInfoToRedis(ctx *reqctx.Context, appID string, roomID int64, info *model.ChatroomInfo) error {
	key := rediskey.ChatroomInfo(appID, roomID)
	rdb.Del(ctx, key)
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, key, b, 0).Err()
}
