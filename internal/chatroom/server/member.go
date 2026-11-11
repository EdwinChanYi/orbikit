package server

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/lock"
	"github.com/yourname/roomkit/internal/chatroom/logic"
	"github.com/yourname/roomkit/internal/chatroom/reqctx"
)

// downAllMics frees every seat of uid after a block or kick.
func downAllMics(ctx *reqctx.Context, name, appID string, roomID, uid int64, reason servicepb.OnMicInfo_OffReason,
	r **servicepb.UnifiedResponse) (bool, error) {
	space := logic.MakeMicSpace(ctx, appID, roomID)
	code, err := space.Init()
	if failed(ctx, name+" space.Init", code, err, r) {
		return false, err
	}
	code, err = space.DownAll(uid, reason)
	if failed(ctx, name+" space.DownAll", code, err, r) {
		return false, err
	}
	return true, nil
}

// BlockChatroomMember blocks (action != 0) or unblocks a member.
func (s *Server) BlockChatroomMember(goCtx context.Context, req *servicepb.BlockChatroomMemberRequest) (reply *servicepb.BlockChatroomMemberReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.BlockChatroomMemberReply{Ret: serviceErr()}
	ctx.Info("rpc BlockChatroomMember", zap.Int64("roomID", req.GetRoomId()), zap.Int64("from", req.GetFrom()),
		zap.Int64("to", req.GetTo()), zap.Int32("action", req.GetAction()), zap.Int("extLen", len(req.GetExt())))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "BlockChatroomMember", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "BlockChatroomMember", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	appID, roomID := req.GetAppId(), req.GetRoomId()
	if !lock.AcquireRoomProcess(ctx, appID, roomID) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomProcess(appID, roomID)

	room, err := openRoom(ctx, "BlockChatroomMember", appID, roomID, &reply.Ret)
	if room == nil {
		return
	}
	code, err := room.BlockChatroomMember(req.GetFrom(), req.GetTo(), req.GetAction(), req.GetExt())
	if failed(ctx, "BlockChatroomMember", code, err, &reply.Ret) {
		return
	}
	if req.GetAction() != 0 {
		if ok, err = downAllMics(ctx, "BlockChatroomMember", appID, roomID, req.GetTo(), servicepb.OnMicInfo_BLOCKED_ROOM, &reply.Ret); !ok {
			return
		}
	}
	if err = room.SaveOptExt(req.GetTo(), req.GetExt()); err != nil {
		ctx.Error("BlockChatroomMember SaveOptExt err", zap.Error(err))
		return
	}
	reply.Ret = success()
	return
}

// KickChatroomMember kicks (action != 0) or unkicks a member.
func (s *Server) KickChatroomMember(goCtx context.Context, req *servicepb.KickChatroomMemberRequest) (reply *servicepb.KickChatroomMemberReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.KickChatroomMemberReply{Ret: serviceErr()}
	ctx.Info("rpc KickChatroomMember", zap.Int64("roomID", req.GetRoomId()), zap.Int64("from", req.GetFrom()),
		zap.Int64("to", req.GetTo()), zap.Int32("action", req.GetAction()), zap.Int64("time", req.GetTime()),
		zap.Int("extLen", len(req.GetExt())))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "KickChatroomMember", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "KickChatroomMember", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	appID, roomID := req.GetAppId(), req.GetRoomId()
	if !lock.AcquireRoomProcess(ctx, appID, roomID) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomProcess(appID, roomID)

	room, err := openRoom(ctx, "KickChatroomMember", appID, roomID, &reply.Ret)
	if room == nil {
		return
	}
	code, err := room.KickChatroomMember(req.GetFrom(), req.GetTo(), req.GetAction(), req.GetTime(), req.GetExt(), req.GetNotifyExt())
	if failed(ctx, "KickChatroomMember", code, err, &reply.Ret) {
		return
	}
	if req.GetAction() != 0 {
		if ok, err = downAllMics(ctx, "KickChatroomMember", appID, roomID, req.GetTo(), servicepb.OnMicInfo_KICKED_ROOM, &reply.Ret); !ok {
			return
		}
	}
	if err = room.SaveOptExt(req.GetTo(), req.GetExt()); err != nil {
		ctx.Error("KickChatroomMember SaveOptExt err", zap.Error(err))
		return
	}
	reply.Ret = success()
	return
}

// MuteChatroomMember mutes (action != 0) or unmutes a member.
func (s *Server) MuteChatroomMember(goCtx context.Context, req *servicepb.MuteChatroomMemberRequest) (reply *servicepb.MuteChatroomMemberReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.MuteChatroomMemberReply{Ret: serviceErr()}
	ctx.Info("rpc MuteChatroomMember", zap.Int64("roomID", req.GetRoomId()), zap.Int64("from", req.GetFrom()),
		zap.Int64("to", req.GetTo()), zap.Int32("action", req.GetAction()), zap.Int64("time", req.GetTime()),
		zap.Int("extLen", len(req.GetExt())))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "MuteChatroomMember", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "MuteChatroomMember", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	appID, roomID := req.GetAppId(), req.GetRoomId()
	if !lock.AcquireRoomProcess(ctx, appID, roomID) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomProcess(appID, roomID)

	room, err := openRoom(ctx, "MuteChatroomMember", appID, roomID, &reply.Ret)
	if room == nil {
		return
	}
	code, err := room.MuteChatroomMember(req.GetFrom(), req.GetTo(), req.GetAction(), req.GetTime())
	if failed(ctx, "MuteChatroomMember", code, err, &reply.Ret) {
		return
	}
	if err = room.SaveOptExt(req.GetTo(), req.GetExt()); err != nil {
		ctx.Error("MuteChatroomMember SaveOptExt err", zap.Error(err))
		return
	}
	reply.Ret = success()
	return
}

// UpdateUserChatroomInfo replaces a member's profile in a room.
func (s *Server) UpdateUserChatroomInfo(goCtx context.Context, req *servicepb.UpdateUserChatroomInfoRequest) (reply *servicepb.UpdateUserChatroomInfoReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UpdateUserChatroomInfoReply{Ret: serviceErr()}
	info := req.GetUserInfo()
	if info == nil || info.GetUid() == 0 {
		reply.Ret = paramErr()
		return reply, errors.New("UpdateUserChatroomInfo bad param")
	}
	ctx.Info("rpc UpdateUserChatroomInfo", zap.Int64("roomID", req.GetChatroomId()), zap.Int64("uid", info.GetUid()),
		zap.Int32s("roleIDs", info.GetRoleId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "UpdateUserChatroomInfo", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "UpdateUserChatroomInfo", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "UpdateUserChatroomInfo", req.GetAppId(), req.GetChatroomId(), &reply.Ret)
	if room == nil {
		return
	}
	code, err := room.ModifyChatroomUserInfo(info.GetUid(), info)
	if failed(ctx, "UpdateUserChatroomInfo ModifyChatroomUserInfo", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// FetchHumanChatroomMember lists the human members of a room.
func (s *Server) FetchHumanChatroomMember(goCtx context.Context, req *servicepb.FetchHumanChatroomMemberRequest) (reply *servicepb.FetchHumanChatroomMemberReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.FetchHumanChatroomMemberReply{Ret: serviceErr()}
	defer recoverTo(ctx, "FetchHumanChatroomMember", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "FetchHumanChatroomMember", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "FetchHumanChatroomMember", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	code, members, err := room.QueryHumanMemberInfo(req.GetSortType(), req.GetEnterTime(), req.GetLimit(),
		req.GetIsDetail(), req.GetIsOnline())
	reply.Members = members
	if failed(ctx, "FetchHumanChatroomMember QueryHumanMemberInfo", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// fetchMembers runs a member list query on an open room and fills reply.
func fetchMembers(goCtx context.Context, name, traceID, appID string, roomID int64,
	query func(*logic.ChatroomContainer) (servicepb.ErrorCode, []logic.MemberDetail, error)) (reply *servicepb.FetchChatroomMemberReply, err error) {
	ctx := newCtx(goCtx, traceID, appID)
	reply = &servicepb.FetchChatroomMemberReply{Ret: serviceErr()}
	defer recoverTo(ctx, name, &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, name, appID, &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, name, appID, roomID, &reply.Ret)
	if room == nil {
		return
	}
	code, users, err := query(room)
	if failed(ctx, name+" query", code, err, &reply.Ret) {
		return
	}
	memberReply(reply, users)
	reply.Ret = success()
	return
}

// FetchChatroomMemberByType lists members by status (blocked, muted,
// kicked, online, robot).
func (s *Server) FetchChatroomMemberByType(goCtx context.Context, req *servicepb.FetchChatroomMemberByTypeRequest) (*servicepb.FetchChatroomMemberReply, error) {
	return fetchMembers(goCtx, "FetchChatroomMemberByType", req.GetMsgTraceId(), req.GetAppId(), req.GetRoomId(),
		func(room *logic.ChatroomContainer) (servicepb.ErrorCode, []logic.MemberDetail, error) {
			return room.QueryMemberInfoByType(req.GetUid(), req.GetSortType(), req.GetEnterTime(), req.GetLimit(),
				req.GetType(), req.GetFoundRobot())
		})
}

// FetchChatroomMemberByRole lists members holding any of the given roles.
func (s *Server) FetchChatroomMemberByRole(goCtx context.Context, req *servicepb.FetchChatroomMemberByRoleRequest) (*servicepb.FetchChatroomMemberReply, error) {
	return fetchMembers(goCtx, "FetchChatroomMemberByRole", req.GetMsgTraceId(), req.GetAppId(), req.GetRoomId(),
		func(room *logic.ChatroomContainer) (servicepb.ErrorCode, []logic.MemberDetail, error) {
			return room.QueryMemberInfoByRole(req.GetSortType(), req.GetEnterTime(), req.GetLimit(), req.GetRole(),
				req.GetIsOnline(), req.GetFoundRobot())
		})
}

// FetchChatroomMemberById returns the given members.
func (s *Server) FetchChatroomMemberById(goCtx context.Context, req *servicepb.FetchChatroomMemberByIdRequest) (*servicepb.FetchChatroomMemberReply, error) {
	return fetchMembers(goCtx, "FetchChatroomMemberById", req.GetMsgTraceId(), req.GetAppId(), req.GetRoomId(),
		func(room *logic.ChatroomContainer) (servicepb.ErrorCode, []logic.MemberDetail, error) {
			return room.QueryMemberInfoById(req.GetSortType(), 0, 0, req.GetQueryId())
		})
}

// FetchChatroomMemberByKeyword searches members by uid, nickname or search keys.
func (s *Server) FetchChatroomMemberByKeyword(goCtx context.Context, req *servicepb.FetchChatroomMemberByKeywordRequest) (*servicepb.FetchChatroomMemberReply, error) {
	return fetchMembers(goCtx, "FetchChatroomMemberByKeyword", req.GetMsgTraceId(), req.GetAppId(), req.GetRoomId(),
		func(room *logic.ChatroomContainer) (servicepb.ErrorCode, []logic.MemberDetail, error) {
			return room.QueryMemberInfoByKeyword(req.GetSortType(), req.GetEnterTime(), req.GetLimit(), req.GetKeyword())
		})
}

// GetUserCurrentChatroomList returns the rooms the user is in.
func (s *Server) GetUserCurrentChatroomList(goCtx context.Context, req *servicepb.GetUserCurrentChatroomListRequest) (reply *servicepb.GetUserCurrentChatroomListReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.GetUserCurrentChatroomListReply{Ret: serviceErr()}
	ctx.Debug("rpc GetUserCurrentChatroomList", zap.Int64("uid", req.GetUid()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "GetUserCurrentChatroomList", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "GetUserCurrentChatroomList", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	if reply.RoomInfo, err = logic.GetChatroomsByAppIdUid(ctx, req.GetAppId(), req.GetUid()); err != nil {
		ctx.Error("GetUserCurrentChatroomList GetChatroomsByAppIdUid err", zap.Error(err))
		return
	}
	reply.Ret = success()
	return
}
