package logic

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
)

const (
	evUserOnline   = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_USERONLINE
	evUserOffline  = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_USEROFFLINE
	evEnter        = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ENTER
	evExit         = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_EXIT
	evAddBlack     = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDBLACK
	evRemoveBlack  = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEBLACK
	evAddMute      = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDMUTE
	evRemoveMute   = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEMUTE
	evRoomMuted    = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ROOMMUTED
	evRoomUnmuted  = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ROOMUNMUTED
	evMicStatus    = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROSTATUSCHANGE
	evMicChange    = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROCHANGE
	evMemberUpdate = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MEMBERUPDATEINFO
	evKicked       = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_KICKED
	evUnkicked     = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_UNKICKED
	evInfoUpdated  = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_INFOUPDATED
	evRoleChange   = servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ROLECHANGE
)

func onlineMembers(ctx context.Context, appID string, roomID int64) ([]int64, error) {
	set, err := rdb.SMembers(ctx, rediskey.ChatroomOnlineMember(appID, roomID)).Result()
	if err != nil {
		return nil, err
	}
	uids := make([]int64, 0, len(set))
	for _, v := range set {
		uid, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, err
		}
		uids = append(uids, uid)
	}
	return uids, nil
}

// makeEventBcst builds a system event addressed to every online member.
func makeEventBcst(ctx context.Context, typ servicepb.ChatroomEventType, appID string, roomID int64, msgID uint64,
	from, to int64, fromInfo, toInfo *servicepb.ChatroomMemberInfo, content map[string][]byte) (*servicepb.ChatroomBcst, error) {
	id := strconv.FormatUint(msgID, 10)
	msg := &servicepb.ChatroomBcst{
		AppId:      appID,
		ChatroomId: roomID,
		ClientType: servicepb.ClientType_CLIENT_TYPE_SYSTEM,
		MsgType:    servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_EVENT,
		MsgId:      id,
		CreateTime: nowMillis(),
		MsgTraceId: id,
	}
	uids, err := onlineMembers(ctx, appID, roomID)
	if err != nil {
		return nil, err
	}
	msg.IncludedUid = uids

	ev := &servicepb.ChatroomEvent{Type: typ, From: from, To: to}
	if len(content) > 0 {
		ev.EventContent = content
	}
	if from != 0 {
		ev.FromMemberInfo = fromInfo
	}
	if to != 0 {
		ev.ToMemberInfo = toInfo
	}
	msg.Content = &servicepb.ChatroomBcst_EventContent{EventContent: ev}
	return msg, nil
}

// broadcastEvent sends a system event to every online member of a room.
func broadcastEvent(ctx context.Context, typ servicepb.ChatroomEventType, appID string, roomID int64, msgID uint64,
	from, to int64, fromInfo, toInfo *servicepb.ChatroomMemberInfo, content map[string][]byte) (*servicepb.ChatroomBcst, error) {
	msg, err := makeEventBcst(ctx, typ, appID, roomID, msgID, from, to, fromInfo, toInfo, content)
	if err != nil {
		return nil, err
	}
	go events.Broadcast(msg)
	return msg, nil
}

// broadcastRoleChange sends a role change event carrying the old and new role IDs.
func broadcastRoleChange(ctx context.Context, appID string, roomID int64, msgID uint64, to int64,
	toInfo *servicepb.ChatroomMemberInfo, notify map[string]string) error {
	msg, err := makeEventBcst(ctx, evRoleChange, appID, roomID, msgID, 0, to, nil, toInfo, nil)
	if err != nil {
		return err
	}
	msg.GetEventContent().NotifyData = notify
	go events.Broadcast(msg)
	return nil
}

// BroadcastUnkicked notifies a room that a member's kick expired.
func BroadcastUnkicked(ctx *reqctx.Context, appID string, roomID, uid int64, info *servicepb.ChatroomMemberInfo) error {
	_, err := broadcastEvent(ctx, evUnkicked, appID, roomID, ctx.MsgID(), 0, uid, nil, info, nil)
	return err
}

// BroadcastUnmuted notifies a room that a member's mute expired.
func BroadcastUnmuted(ctx *reqctx.Context, appID string, roomID, uid int64, info *servicepb.ChatroomMemberInfo) error {
	_, err := broadcastEvent(ctx, evRemoveMute, appID, roomID, ctx.MsgID(), 0, uid, nil, info, nil)
	return err
}

// BroadcastEnter notifies a room that uid entered.
func BroadcastEnter(ctx *reqctx.Context, appID string, roomID, uid int64, info *servicepb.ChatroomMemberInfo) error {
	_, err := broadcastEvent(ctx, evEnter, appID, roomID, ctx.MsgID(), uid, 0, info, nil, nil)
	return err
}

// BroadcastExit notifies a room that uid left.
func BroadcastExit(ctx *reqctx.Context, appID string, roomID, uid int64, info *servicepb.ChatroomMemberInfo) error {
	_, err := broadcastEvent(ctx, evExit, appID, roomID, ctx.MsgID(), uid, 0, info, nil, nil)
	return err
}

// BroadcastInfoUpdated notifies a room that its info changed; content is
// the room info as returned by GetChatroomInfoToMap.
func BroadcastInfoUpdated(ctx *reqctx.Context, appID string, roomID int64, content map[string][]byte) error {
	_, err := broadcastEvent(ctx, evInfoUpdated, appID, roomID, ctx.MsgID(), 0, 0, nil, nil, content)
	return err
}

// BroadcastMicChange notifies a room that the set of mic seats changed;
// content is the layout returned by MicSpace.GetMicInfo.
func BroadcastMicChange(ctx *reqctx.Context, appID string, roomID int64, content map[string][]byte) error {
	_, err := broadcastEvent(ctx, evMicChange, appID, roomID, ctx.MsgID(), 0, 0, nil, nil, content)
	return err
}

// BroadcastMicStatus notifies a room that seat states changed; content is
// the layout returned by MicSpace.GetMicInfo.
func BroadcastMicStatus(ctx *reqctx.Context, appID string, roomID int64, content map[string][]byte) error {
	_, err := broadcastEvent(ctx, evMicStatus, appID, roomID, ctx.MsgID(), 0, 0, nil, nil, content)
	return err
}

func makeReqToRspBcstMsg(ctx *reqctx.Context, req *servicepb.ChatroomMsgRequest) (*servicepb.ChatroomBcst, error) {
	id := strconv.FormatUint(ctx.MsgID(), 10)
	msg := &servicepb.ChatroomBcst{
		AppId:      req.AppId,
		From:       req.From,
		To:         req.To,
		ChatroomId: req.ChatroomId,
		ClientType: req.ClientType,
		MsgType:    req.MsgType,
		MsgId:      id,
		CreateTime: nowMillis(),
		MsgTraceId: id,
	}
	switch req.MsgType {
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT:
		msg.Content = &servicepb.ChatroomBcst_TextContent{TextContent: req.GetTextContent()}
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM:
		msg.Content = &servicepb.ChatroomBcst_CustomContent{CustomContent: req.GetCustomContent()}
	}
	if len(req.Ext) != 0 {
		msg.Ext = req.Ext
	}

	c, err := GetChatroom(ctx, req.AppId, req.ChatroomId)
	if err != nil {
		return nil, err
	}
	memberInfo := func(uid int64) (*servicepb.ChatroomMemberInfo, error) {
		errCode, info, err := c.GetChatroomMemberInfo(uid)
		if err == nil && errCode != servicepb.ErrorCode_SUCCESS {
			err = fmt.Errorf("get chatroom member info errCode:%d", errCode)
		}
		return info, err
	}
	if req.From != 0 {
		if msg.FromMemberInfo, err = memberInfo(req.From); err != nil {
			return nil, err
		}
	}
	if req.To != 0 {
		msg.IncludedUid = append(msg.IncludedUid, req.To)
		if msg.ToMemberInfo, err = memberInfo(req.To); err != nil {
			return nil, err
		}
		return msg, nil
	}

	uids, err := onlineMembers(ctx, c.appId, c.roomId)
	if err != nil {
		return nil, err
	}
	for _, uid := range uids {
		if uid != req.From {
			msg.IncludedUid = append(msg.IncludedUid, uid)
		}
	}
	return msg, nil
}

// SendMsg publishes a member message to the room broadcast topic.
func SendMsg(ctx *reqctx.Context, req *servicepb.ChatroomMsgRequest) (servicepb.ErrorCode, error) {
	msg, err := makeReqToRspBcstMsg(ctx, req)
	if err != nil {
		ctx.Error("SendMsg make msg err", zap.Error(err))
		return servicepb.ErrorCode_SERVER_INTERNAL_ERROR, err
	}
	go events.Broadcast(msg)
	return servicepb.ErrorCode_SUCCESS, nil
}

// SendFullServiceBroadcastMsg publishes a message to every online user of the app.
func SendFullServiceBroadcastMsg(ctx *reqctx.Context, req *servicepb.FullServiceBroadcastMsgRequest) (servicepb.ErrorCode, error) {
	id := strconv.FormatUint(ctx.MsgID(), 10)
	msg := &servicepb.ChatroomBcst{
		AppId:          req.AppId,
		From:           req.From,
		ClientType:     req.ClientType,
		MsgType:        req.MsgType,
		MsgId:          id,
		CreateTime:     time.Now().UnixNano() / int64(time.Millisecond),
		FromMemberInfo: req.FromUserInfo,
		MsgTraceId:     id,
	}
	switch req.MsgType {
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT:
		msg.Content = &servicepb.ChatroomBcst_TextContent{TextContent: req.GetTextContent()}
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM:
		msg.Content = &servicepb.ChatroomBcst_CustomContent{CustomContent: req.GetCustomContent()}
	}
	if len(req.Ext) != 0 {
		msg.Ext = req.Ext
	}
	go events.FullServer(msg)
	return servicepb.ErrorCode_SUCCESS, nil
}
