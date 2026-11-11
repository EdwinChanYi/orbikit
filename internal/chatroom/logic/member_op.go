package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
)

const (
	kickedReasonKeep = 10
	coreCallTimeout  = 5 * time.Second
)

// CheckUserPermission checks p for from against to; from == 0 is a system call.
func (c *ChatroomContainer) CheckUserPermission(from, to int64, p servicepb.Privilege) (servicepb.ErrorCode, error) {
	if from == 0 {
		return servicepb.ErrorCode_SUCCESS, nil
	}
	return checkUserPermission(c.ctx, c.appId, c.roomId, from, to, p, servicepb.ErrorCode_CHATROOM_USER_PERMISSION_ERROR)
}

// kickedReason formats an entry of the kicked reason list.
func kickedReason(at time.Time, from int64, reason servicepb.ChatroomKickReason) string {
	return fmt.Sprintf("%d,%d,%d", at.UnixNano(), from, int32(reason))
}

// pushKickedReason appends to the kicked reason list, keeping the latest ten.
func pushKickedReason(ctx context.Context, appID string, roomID, uid, from int64, reason servicepb.ChatroomKickReason) error {
	key := rediskey.ChatroomKickedReason(appID, roomID, uid)
	n, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		return err
	}
	if n >= kickedReasonKeep {
		if err := rdb.LPop(ctx, key).Err(); err != nil {
			return err
		}
	}
	return rdb.RPush(ctx, key, kickedReason(time.Now(), from, reason)).Err()
}

// operatorInfo loads the operator's member info and checks they are in the room.
func (c *ChatroomContainer) operatorInfo(from int64) (servicepb.ErrorCode, *servicepb.ChatroomMemberInfo, error) {
	if from == 0 {
		return servicepb.ErrorCode_SUCCESS, nil, nil
	}
	errCode, info, err := c.GetChatroomMemberInfo(from)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil, nil
	}
	if errCode, err = c.IsUserInChatroom(from); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, nil, err
	}
	return errCode, info, nil
}

// KickChatroomMember kicks (action == 1) or unkicks a member.
func (c *ChatroomContainer) KickChatroomMember(from, to int64, action int32, timeout int64,
	reason, notifyExt map[string]string) (servicepb.ErrorCode, error) {
	if errCode, err := c.CheckUserPermission(from, to, servicepb.Privilege_KICK); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	errCode, fromInfo, err := c.operatorInfo(from)
	if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}

	if action != 1 {
		if err := UnkickMember(c.ctx, c.appId, c.roomId, to); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if _, err := broadcastEvent(c.ctx, evUnkicked, c.appId, c.roomId, c.ctx.MsgID(), from, to, nil, nil, nil); err != nil {
			c.ctx.Error("KickChatroomMember broadcast unkicked error", zap.Error(err))
		}
		return servicepb.ErrorCode_SUCCESS, nil
	}

	if err := kickMember(c.ctx, c.appId, c.roomId, to, timeout); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	errCode, toInfo, err := c.GetChatroomMemberInfo(to)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	if errCode, err = c.IsUserInChatroom(to); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	if errCode, err = c.Leave(to, servicepb.AtRoomInfo_KICKED); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	if err := pushKickedReason(c.ctx, c.appId, c.roomId, to, from, servicepb.ChatroomKickReason_KICK_BY_PERSON); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	events.Kicked(&servicepb.ChatRoomMemberBeKickedBcst{
		AppId:        c.appId,
		RoomId:       c.roomId,
		KickedReason: servicepb.ChatroomKickReason_KICK_BY_PERSON,
		Duration:     timeout,
		RemoteExt:    reason,
		NotifyExt:    notifyExt,
		ToUid:        []int64{to},
	})
	if _, err := broadcastEvent(c.ctx, evKicked, c.appId, c.roomId, c.ctx.MsgID(), from, to, fromInfo, toInfo, nil); err != nil {
		c.ctx.Error("KickChatroomMember broadcast kicked error", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// IsChatroomMemberMuted reports CHATROOM_USER_IS_MUTED for a muted member.
func (c *ChatroomContainer) IsChatroomMemberMuted(uid int64) (servicepb.ErrorCode, error) {
	n, _ := rdb.Get(c.ctx, rediskey.ChatroomMutedUser(c.appId, c.roomId, uid)).Int()
	if n == 1 {
		return servicepb.ErrorCode_CHATROOM_USER_IS_MUTED, nil
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// MuteChatroomMember mutes (action == 1) or unmutes a member.
func (c *ChatroomContainer) MuteChatroomMember(from, to int64, action int32, timeout int64) (servicepb.ErrorCode, error) {
	if errCode, err := c.CheckUserPermission(from, to, servicepb.Privilege_MUTE); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	errCode, fromInfo, err := c.operatorInfo(from)
	if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	errCode, toInfo, err := c.GetChatroomMemberInfo(to)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	if action == 1 {
		if errCode, err := c.IsUserInChatroom(to); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		} else if errCode != servicepb.ErrorCode_SUCCESS {
			c.ctx.Debug("MuteChatroomMember IsUserInChatroom errCode", zap.Int("errCode", int(errCode)))
		}
		if err := muteMember(c.ctx, c.appId, c.roomId, to, timeout); err != nil {
			c.ctx.Error("MuteChatroomMember muteMember err", zap.Error(err))
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if _, err := broadcastEvent(c.ctx, evAddMute, c.appId, c.roomId, c.ctx.MsgID(), from, to, fromInfo, toInfo, nil); err != nil {
			c.ctx.Error("MuteChatroomMember broadcast add mute err", zap.Error(err))
		}
		return servicepb.ErrorCode_SUCCESS, nil
	}

	if err := unmuteMember(c.ctx, c.appId, c.roomId, to); err != nil {
		c.ctx.Error("MuteChatroomMember unmuteMember err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if _, err := broadcastEvent(c.ctx, evRemoveMute, c.appId, c.roomId, c.ctx.MsgID(), from, to, fromInfo, toInfo, nil); err != nil {
		c.ctx.Error("MuteChatroomMember broadcast remove mute err", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// BlockChatroomMember blocks (action == 1) or unblocks a member. Blocking
// also removes the member from the room.
func (c *ChatroomContainer) BlockChatroomMember(from, to int64, action int32, notifyExt map[string]string) (servicepb.ErrorCode, error) {
	if errCode, err := c.CheckUserPermission(from, to, servicepb.Privilege_BLOCK); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}
	errCode, fromInfo, err := c.operatorInfo(from)
	if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, err
	}
	errCode, toInfo, err := c.GetChatroomMemberInfo(to)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	if action != 1 {
		if err := unblockMember(c.appId, c.roomId, to); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		}
		if _, err := broadcastEvent(c.ctx, evRemoveBlack, c.appId, c.roomId, c.ctx.MsgID(), from, to, fromInfo, toInfo, nil); err != nil {
			c.ctx.Error("BlockChatroomMember broadcast unblock err", zap.Error(err))
		}
		return servicepb.ErrorCode_SUCCESS, nil
	}

	inRoom, err := c.IsUserInChatroom(to)
	if err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if err := blockMember(c.appId, c.roomId, to); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if inRoom == servicepb.ErrorCode_SUCCESS {
		if errCode, err := c.Leave(to, servicepb.AtRoomInfo_BLOCKED); err != nil {
			return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
		} else if errCode != servicepb.ErrorCode_SUCCESS {
			return errCode, nil
		}
	}
	if err := pushKickedReason(c.ctx, c.appId, c.roomId, to, from, servicepb.ChatroomKickReason_KICK_BY_BLACKLIST); err != nil {
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}

	events.Kicked(&servicepb.ChatRoomMemberBeKickedBcst{
		AppId:        c.appId,
		RoomId:       c.roomId,
		KickedReason: servicepb.ChatroomKickReason_KICK_BY_BLACKLIST,
		NotifyExt:    notifyExt,
		ToUid:        []int64{to},
	})
	if _, err := broadcastEvent(c.ctx, evAddBlack, c.appId, c.roomId, c.ctx.MsgID(), from, to, fromInfo, toInfo, nil); err != nil {
		c.ctx.Error("BlockChatroomMember broadcast block err", zap.Error(err))
	}
	return servicepb.ErrorCode_SUCCESS, nil
}

// SaveOptExt stores the ext attached to an operation on uid.
func (c *ChatroomContainer) SaveOptExt(uid int64, ext map[string]string) error {
	if len(ext) == 0 {
		return nil
	}
	key := rediskey.ChatroomMemberOptExt(c.appId, c.roomId, uid)
	rdb.Del(c.ctx, key)
	b, err := json.Marshal(ext)
	if err != nil {
		c.ctx.Error("SaveOptExt Marshal err", zap.Error(err))
		return err
	}
	return rdb.Set(c.ctx, key, b, 0).Err()
}

// GetOptExt loads the ext stored by SaveOptExt.
func (c *ChatroomContainer) GetOptExt(uid int64) (map[string]string, servicepb.ErrorCode, error) {
	data, err := rdb.Get(c.ctx, rediskey.ChatroomMemberOptExt(c.appId, c.roomId, uid)).Result()
	if err != nil {
		if isNil(err) {
			return nil, servicepb.ErrorCode_CHATROOM_MEMBER_OPTEXT_NOT_EXISTS, nil
		}
		return nil, servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	ext := make(map[string]string)
	if err := json.Unmarshal([]byte(data), &ext); err != nil {
		c.ctx.Error("GetOptExt Unmarshal err", zap.Error(err))
		return nil, servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	return ext, servicepb.ErrorCode_SUCCESS, nil
}

// RestrainUser bans (action == 1) or unbans a user through the core service
// and publishes a MISC_EVENT_USER_RESTRAIN event on ban.
func RestrainUser(ctx *reqctx.Context, appID string, uid int64, action int32) (servicepb.ErrorCode, error) {
	cctx, cancel := context.WithTimeout(ctx, coreCallTimeout)
	defer cancel()
	reply, err := core.RestraintUserOperator(cctx, &servicepb.RestraintUserOperatorRequest{
		AppId: appID, Uid: uid, Action: action,
	})
	if err != nil {
		ctx.Error("RestrainUser RestraintUserOperator err", zap.Error(err))
		return servicepb.ErrorCode_CHATROOM_SERVICE_ERROR, err
	}
	if errCode := reply.GetRet().GetErrorCode(); errCode != servicepb.ErrorCode_SUCCESS {
		return errCode, nil
	}

	if action == 1 {
		if err := events.Misc(&servicepb.ChatRoomMiscEventMsg{
			Type:  servicepb.ChatRoomMiscEventType_MISC_EVENT_USER_RESTRAIN,
			AppId: appID,
			Content: &servicepb.ChatRoomMiscEventMsg_RestrainInfo{
				RestrainInfo: &servicepb.RestrainUserMsg{Uid: uid},
			},
		}); err != nil {
			ctx.Error("RestrainUser send misc event err", zap.Error(err))
			return servicepb.ErrorCode_SUCCESS, err
		}
	}
	return servicepb.ErrorCode_SUCCESS, nil
}
