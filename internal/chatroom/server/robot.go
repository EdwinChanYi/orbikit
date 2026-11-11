package server

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/lock"
	"github.com/yourname/roomkit/internal/chatroom/logic"
	"github.com/yourname/roomkit/internal/chatroom/reqctx"
)

const robotBatchTimeout = time.Minute

// AddRobotUserInfo registers or replaces a robot profile.
func (s *Server) AddRobotUserInfo(goCtx context.Context, req *servicepb.AddRobotUserInfoRequest) (reply *servicepb.AddRobotUserInfoReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.AddRobotUserInfoReply{Ret: serviceErr()}
	info := req.GetInfo()
	if info == nil {
		reply.Ret = paramErr()
		return
	}
	ctx.Info("rpc AddRobotUserInfo", zap.Int64("uid", info.GetUid()), zap.String("name", info.GetNickname()),
		zap.Int32s("roleIDs", info.GetRoleId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "AddRobotUserInfo", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "AddRobotUserInfo", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	code, err := logic.AddRobotInfo(req.GetAppId(), info)
	if failed(ctx, "AddRobotUserInfo AddRobotInfo", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// RemoveRobotUserInfo is accepted for compatibility; robot profiles are kept.
func (s *Server) RemoveRobotUserInfo(goCtx context.Context, req *servicepb.RemoveRobotUserInfoRequest) (reply *servicepb.RemoveRobotUserInfoReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.RemoveRobotUserInfoReply{Ret: serviceErr()}
	ctx.Info("rpc RemoveRobotUserInfo", zap.Int64("uid", req.GetUid()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "RemoveRobotUserInfo", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "RemoveRobotUserInfo", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	code, err := logic.DelRobotInfo(req.GetAppId(), req.GetUid())
	if failed(ctx, "RemoveRobotUserInfo DelRobotInfo", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// lockedOpenRoom checks the app, takes the room process lock and loads the
// open room. On success the caller must call the returned release function.
func lockedOpenRoom(ctx *reqctx.Context, name, appID string, roomID int64, r **servicepb.UnifiedResponse) (*logic.ChatroomContainer, func(), error) {
	return prepareMic(ctx, micRequest{name: name, appID: appID, roomID: roomID}, r)
}

// AddRobotToChatroom puts a robot into a room.
func (s *Server) AddRobotToChatroom(goCtx context.Context, req *servicepb.AddRobotToChatroomRequest) (reply *servicepb.AddRobotToChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.AddRobotToChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc AddRobotToChatroom", zap.Int64("roomID", req.GetChatroomId()), zap.Int64("uid", req.GetUid()),
		zap.Int32s("roleIDs", req.GetRoleId()), zap.Bool("isBroadcast", req.GetIsBroadcast()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "AddRobotToChatroom", &reply.Ret)

	appID, roomID := req.GetAppId(), req.GetChatroomId()
	room, release, err := lockedOpenRoom(ctx, "AddRobotToChatroom", appID, roomID, &reply.Ret)
	if room == nil {
		return
	}
	defer release()

	code, err := room.RobotEnter(req.GetUid(), req.GetRoleId())
	if failed(ctx, "AddRobotToChatroom RobotEnter", code, err, &reply.Ret) {
		return
	}
	if req.GetIsBroadcast() {
		code, member, e := room.GetChatRoomMemberInfoMore(req.GetUid())
		if failed(ctx, "AddRobotToChatroom GetChatRoomMemberInfoMore", code, e, &reply.Ret) {
			return reply, e
		}
		if err = logic.BroadcastEnter(ctx, appID, roomID, req.GetUid(), member); err != nil {
			ctx.Error("AddRobotToChatroom broadcast err", zap.Error(err))
		}
	}
	reply.Ret = success()
	return
}

// BatchAddRobotToChatroom puts several robots into a room. The robots are
// added in the background after the reply is sent.
func (s *Server) BatchAddRobotToChatroom(goCtx context.Context, req *servicepb.BatchAddRobotToChatroomRequest) (reply *servicepb.BatchAddRobotToChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.BatchAddRobotToChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc BatchAddRobotToChatroom", zap.Int64("roomID", req.GetChatroomId()), zap.Int("robots", len(req.GetRobots())),
		zap.Bool("isBroadcast", req.GetIsBroadcast()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "BatchAddRobotToChatroom", &reply.Ret)

	appID, roomID := req.GetAppId(), req.GetChatroomId()
	room, release, err := lockedOpenRoom(ctx, "BatchAddRobotToChatroom", appID, roomID, &reply.Ret)
	if room == nil {
		return
	}
	release()

	robots := make(map[int64][]int32, len(req.GetRobots()))
	for _, r := range req.GetRobots() {
		robots[r.GetUid()] = r.GetRoleId()
	}
	go addRobots(ctx.MsgID(), appID, roomID, robots, req.GetIsBroadcast())

	reply.Ret = success()
	return
}

func addRobots(msgID uint64, appID string, roomID int64, robots map[int64][]int32, broadcast bool) {
	ctx, cancel := reqctx.WithTimeout(msgID, appID, robotBatchTimeout)
	defer cancel()
	defer func() {
		if e := recover(); e != nil {
			ctx.Error("BatchAddRobotToChatroom panic", zap.Any("error", e), zap.Stack("stack"))
		}
	}()
	if !lock.AcquireRoomProcess(ctx, appID, roomID) {
		ctx.Error("BatchAddRobotToChatroom get chatroom lock fail", zap.Int64("roomID", roomID))
		return
	}
	defer lock.ReleaseRoomProcess(appID, roomID)

	room, err := logic.GetChatroom(ctx, appID, roomID)
	if err != nil {
		ctx.Error("BatchAddRobotToChatroom GetChatroom err", zap.Error(err))
		return
	}
	if code, err := room.RobotsEnter(robots); err != nil || code != servicepb.ErrorCode_SUCCESS {
		ctx.Error("BatchAddRobotToChatroom RobotsEnter err", zap.Int("errCode", int(code)), zap.Error(err))
		return
	}
	if !broadcast {
		return
	}
	for uid := range robots {
		code, member, err := room.GetChatRoomMemberInfoMore(uid)
		if err != nil || code != servicepb.ErrorCode_SUCCESS {
			ctx.Error("BatchAddRobotToChatroom GetChatRoomMemberInfoMore err", zap.Int("errCode", int(code)), zap.Error(err))
			return
		}
		if err := logic.BroadcastEnter(ctx, appID, roomID, uid, member); err != nil {
			ctx.Error("BatchAddRobotToChatroom broadcast err", zap.Error(err))
		}
	}
}

// RemoveRobotFromChatroom takes a robot out of a room and off its seats.
func (s *Server) RemoveRobotFromChatroom(goCtx context.Context, req *servicepb.RemoveRobotFromChatroomRequest) (reply *servicepb.RemoveRobotFromChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.RemoveRobotFromChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc RemoveRobotFromChatroom", zap.Int64("roomID", req.GetChatroomId()), zap.Int64("uid", req.GetUid()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "RemoveRobotFromChatroom", &reply.Ret)

	appID, roomID, uid := req.GetAppId(), req.GetChatroomId(), req.GetUid()
	room, release, err := lockedOpenRoom(ctx, "RemoveRobotFromChatroom", appID, roomID, &reply.Ret)
	if room == nil {
		return
	}
	defer release()

	code, member, err := room.GetChatroomMemberInfo(uid)
	if failed(ctx, "RemoveRobotFromChatroom GetChatroomMemberInfo", code, err, &reply.Ret) {
		return
	}
	if code, err = room.RobotLeave(uid); failed(ctx, "RemoveRobotFromChatroom RobotLeave", code, err, &reply.Ret) {
		return
	}
	var ok bool
	if ok, err = downAllMics(ctx, "RemoveRobotFromChatroom", appID, roomID, uid, servicepb.OnMicInfo_LEAVE_ROOM, &reply.Ret); !ok {
		return
	}
	if err = logic.BroadcastExit(ctx, appID, roomID, uid, member); err != nil {
		ctx.Error("RemoveRobotFromChatroom broadcast err", zap.Error(err))
	}
	reply.Ret = success()
	return
}
