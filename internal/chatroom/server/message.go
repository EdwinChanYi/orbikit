package server

import (
	"context"
	"strconv"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/logic"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
)

// canSpeak checks that a user message may be sent: the sender is in the
// open room, the room-wide mute does not apply to them and they are not
// muted individually.
func canSpeak(ctx *reqctx.Context, req *servicepb.ChatroomMsgRequest, r **servicepb.UnifiedResponse) (bool, error) {
	room, err := openRoom(ctx, "SendChatroomMsg", req.GetAppId(), req.GetChatroomId(), r)
	if room == nil {
		return false, err
	}
	code, err := room.IsUserInChatroom(req.GetFrom())
	if failed(ctx, "SendChatroomMsg IsUserInChatroom", code, err, r) {
		return false, err
	}
	code, info, err := room.GetChatroomInfo()
	if failed(ctx, "SendChatroomMsg GetChatroomInfo", code, err, r) {
		return false, err
	}
	if info.GetIsMuteAll() && req.GetFrom() != info.GetOwnerUid() {
		code, err = room.CheckUserPermission(req.GetFrom(), 0, servicepb.Privilege_MUTE_CAN_MESSAGE)
		if err != nil {
			ctx.Error("SendChatroomMsg CheckUserPermission err", zap.Error(err))
			return false, err
		}
		if code != servicepb.ErrorCode_SUCCESS {
			*r = ret(servicepb.ErrorCode_CHATROOM_CHATROOM_IS_MUTED)
			return false, nil
		}
	}
	code, err = room.IsChatroomMemberMuted(req.GetFrom())
	if failed(ctx, "SendChatroomMsg IsChatroomMemberMuted", code, err, r) {
		return false, err
	}
	return true, nil
}

// SendChatroomMsg broadcasts a message to a room. A zero sender is a system
// message and skips the sender checks.
func (s *Server) SendChatroomMsg(goCtx context.Context, req *servicepb.ChatroomMsgRequest) (reply *servicepb.ChatroomMsgReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.ChatroomMsgReply{Ret: serviceErr()}
	ctx.Debug("rpc SendChatroomMsg", zap.Int64("roomID", req.GetChatroomId()), zap.Int64("from", req.GetFrom()),
		zap.Int64("to", req.GetTo()), zap.Int("msgType", int(req.GetMsgType())),
		zap.Int("clientType", int(req.GetClientType())), zap.String("msgID", req.GetMsgId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "SendChatroomMsg", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "SendChatroomMsg", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	if logic.EmptyText(req.GetMsgType(), req.GetTextContent()) {
		reply.Ret = ret(servicepb.ErrorCode_PARAM_ERROR)
		return
	}
	if req.GetFrom() != 0 {
		if ok, err = canSpeak(ctx, req, &reply.Ret); !ok {
			return
		}
	}
	code, err := logic.SendMsg(ctx, req)
	if failed(ctx, "SendChatroomMsg SendMsg", code, err, &reply.Ret) {
		return
	}
	reply.Ret = ret(code)
	reply.MsgId = strconv.FormatUint(ctx.MsgID(), 10)
	return
}

// SendFullServiceBroadcastMsg broadcasts a message to every online user of
// the app.
func (s *Server) SendFullServiceBroadcastMsg(goCtx context.Context, req *servicepb.FullServiceBroadcastMsgRequest) (reply *servicepb.FullServiceBroadcastMsgReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.FullServiceBroadcastMsgReply{Ret: serviceErr()}
	defer recoverTo(ctx, "SendFullServiceBroadcastMsg", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "SendFullServiceBroadcastMsg", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	if logic.EmptyText(req.GetMsgType(), req.GetTextContent()) {
		reply.Ret = ret(servicepb.ErrorCode_PARAM_ERROR)
		return
	}
	code, err := logic.SendFullServiceBroadcastMsg(ctx, req)
	if failed(ctx, "SendFullServiceBroadcastMsg", code, err, &reply.Ret) {
		ctx.Debug("SendFullServiceBroadcastMsg failed", zap.Int64("from", req.GetFrom()),
			zap.Int("msgType", int(req.GetMsgType())), zap.Int("clientType", int(req.GetClientType())))
		return
	}
	reply.Ret = ret(code)
	reply.MsgId = strconv.FormatUint(ctx.MsgID(), 10)
	return
}
