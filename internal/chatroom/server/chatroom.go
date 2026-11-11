package server

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/lock"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/logic"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/reqctx"
)

const closeTimeout = time.Minute

// CreateChatroom creates a room and optionally opens it with a mic layout.
func (s *Server) CreateChatroom(goCtx context.Context, req *servicepb.CreateChatroomRequest) (reply *servicepb.CreateChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.CreateChatroomReply{Ret: serviceErr()}
	info := req.GetChatroomInfo()
	if info == nil || info.GetChatroomId() == 0 || info.GetOwnerUid() == 0 {
		reply.Ret = paramErr()
		return
	}
	roomID := info.GetChatroomId()
	ctx.Info("rpc CreateChatroom", zap.Int64("roomID", roomID), zap.Bool("needOpen", req.GetNeedOpen()),
		zap.Int("spaceCount", len(req.GetSpace())))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "CreateChatroom", &reply.Ret)

	ok, err := logic.IsAppValid(ctx, req.GetAppId())
	if err != nil {
		ctx.Error("CreateChatroom IsAppValid err", zap.Error(err))
		reply.Ret = ret(servicepb.ErrorCode_SERVER_INTERNAL_ERROR)
		return reply, nil
	} else if !ok {
		reply.Ret = ret(servicepb.ErrorCode_CORE_APP_ID_NOT_EXISTS)
		return
	}

	if !lock.AcquireRoomProcess(ctx, req.GetAppId(), roomID) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomProcess(req.GetAppId(), roomID)

	room, err := logic.GetChatroom(ctx, req.GetAppId(), roomID)
	if err != nil {
		ctx.Error("CreateChatroom GetChatroom err", zap.Error(err))
		reply.Ret = ret(servicepb.ErrorCode_SERVER_INTERNAL_ERROR)
		return reply, nil
	}
	code, err := room.CreateChatroom(info)
	if err != nil || code != servicepb.ErrorCode_SUCCESS {
		reply.Ret = ret(code)
		return
	}
	if !req.GetNeedOpen() {
		reply.Ret = success()
		return
	}
	if code, err = room.Open(); err != nil || code != servicepb.ErrorCode_SUCCESS {
		reply.Ret = ret(code)
		return
	}
	reply.Ret, err = createMicSpace(ctx, req.GetAppId(), roomID, req.GetSpace())
	return
}

func createMicSpace(ctx *reqctx.Context, appID string, roomID int64, spaces []*servicepb.SpaceInfos) (*servicepb.UnifiedResponse, error) {
	si := &logic.MicSpaceInfo{AppId: appID, ChatroomId: roomID, ElementInfo: seatsFromSpace(spaces)}
	if _, code, err := logic.MakeMicSpace(ctx, appID, roomID).CreateOrUpdate(si); err != nil {
		return ret(code), err
	}
	return success(), nil
}

// OpenChatroom opens a room and resets its mic layout.
func (s *Server) OpenChatroom(goCtx context.Context, req *servicepb.OpenChatroomRequest) (reply *servicepb.OpenChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.OpenChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc OpenChatroom", zap.Int64("roomID", req.GetRoomId()), zap.Int("spaceCount", len(req.GetSpace())))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "OpenChatroom", &reply.Ret)

	ok, err := logic.IsAppValid(ctx, req.GetAppId())
	if err != nil {
		ctx.Error("OpenChatroom IsAppValid err", zap.Error(err))
		reply.Ret = ret(servicepb.ErrorCode_SERVER_INTERNAL_ERROR)
		return reply, nil
	} else if !ok {
		reply.Ret = ret(servicepb.ErrorCode_CORE_APP_ID_NOT_EXISTS)
		return
	}

	if !lock.AcquireRoomProcess(ctx, req.GetAppId(), req.GetRoomId()) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomProcess(req.GetAppId(), req.GetRoomId())

	room, err := logic.GetChatroom(ctx, req.GetAppId(), req.GetRoomId())
	if err != nil {
		ctx.Error("OpenChatroom GetChatroom err", zap.Error(err))
		return
	}
	code, err := room.Open()
	if err != nil || code != servicepb.ErrorCode_SUCCESS {
		reply.Ret = ret(code)
		return
	}
	reply.Ret, err = createMicSpace(ctx, req.GetAppId(), req.GetRoomId(), req.GetSpace())
	return
}

// CloseChatroom closes a room. The members are removed in the background.
func (s *Server) CloseChatroom(goCtx context.Context, req *servicepb.CloseChatroomRequest) (reply *servicepb.CloseChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.CloseChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc CloseChatroom", zap.Int64("roomID", req.GetRoomId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "CloseChatroom", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "CloseChatroom", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	appID, roomID := req.GetAppId(), req.GetRoomId()
	if !lock.AcquireRoomProcess(ctx, appID, roomID) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomProcess(appID, roomID)

	if room, e := openRoom(ctx, "CloseChatroom", appID, roomID, &reply.Ret); room == nil {
		return reply, e
	}

	go func() {
		bgCtx, cancel := reqctx.WithTimeout(ctx.MsgID(), appID, closeTimeout)
		defer cancel()
		if !lock.AcquireRoomProcess(bgCtx, appID, roomID) {
			bgCtx.Error("CloseChatroom get chatroom lock fail", zap.Int64("roomID", roomID))
			return
		}
		defer lock.ReleaseRoomProcess(appID, roomID)
		room, err := logic.GetChatroom(bgCtx, appID, roomID)
		if err != nil {
			bgCtx.Error("CloseChatroom GetChatroom err", zap.Error(err))
			return
		}
		if code, err := room.Close(); err != nil || code != servicepb.ErrorCode_SUCCESS {
			bgCtx.Error("CloseChatroom Close err", zap.Int("errCode", int(code)), zap.Error(err))
		}
	}()

	reply.Ret = success()
	return
}

// ApplyEnterChatroomToken stages the member info and issues an enter token.
func (s *Server) ApplyEnterChatroomToken(goCtx context.Context, req *servicepb.ApplyEnterChatroomTokenRequest) (reply *servicepb.ApplyEnterChatroomTokenReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.ApplyEnterChatroomTokenReply{Ret: serviceErr()}
	ctx.Info("rpc ApplyEnterChatroomToken", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int32s("roleIDs", req.GetMemberInfo().GetRoleId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "ApplyEnterChatroomToken", &reply.Ret)

	if len(req.GetMemberInfo().GetRoleId()) == 0 {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_APPLAY_TOKEN_ROLEIDS_NOT_EXISTS)
		return
	}
	var ok bool
	if ok, err = checkApp(ctx, "ApplyEnterChatroomToken", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "ApplyEnterChatroomToken", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	code, token, err := room.ApplyToken(req.GetUid(), req.GetMemberInfo())
	if failed(ctx, "ApplyEnterChatroomToken ApplyToken", code, err, &reply.Ret) {
		return
	}
	reply.Token = token
	reply.Ret = success()
	return
}

// EnterChatroom consumes an enter token and announces the new member.
func (s *Server) EnterChatroom(goCtx context.Context, req *servicepb.EnterChatroomRequest) (reply *servicepb.EnterChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.EnterChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc EnterChatroom", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "EnterChatroom", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "EnterChatroom", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "EnterChatroom", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	code, err := room.Enter(req.GetUid(), req.GetToken())
	if failed(ctx, "EnterChatroom Enter", code, err, &reply.Ret) {
		return
	}
	code, roomInfo, err := room.GetChatroomInfo()
	if failed(ctx, "EnterChatroom GetChatroomInfo", code, err, &reply.Ret) {
		return
	}
	reply.RoomInfo = roomInfo
	code, member, err := room.GetChatRoomMemberInfoMore(req.GetUid())
	if failed(ctx, "EnterChatroom GetChatRoomMemberInfoMore", code, err, &reply.Ret) {
		return
	}
	reply.YourselfInfo = member
	reply.Ret = success()

	if err := logic.BroadcastEnter(ctx, req.GetAppId(), req.GetRoomId(), req.GetUid(), member); err != nil {
		ctx.Error("EnterChatroom broadcast err", zap.Error(err))
	}
	return
}

// leaveRoom removes uid from an open room, announces it and frees the
// user's mic seats. *r is set to SUCCESS only when every step succeeds.
func leaveRoom(ctx *reqctx.Context, name string, room *logic.ChatroomContainer, appID string, roomID, uid int64,
	r **servicepb.UnifiedResponse) error {
	code, member, err := room.GetChatroomMemberInfo(uid)
	if failed(ctx, name+" GetChatroomMemberInfo", code, err, r) {
		return err
	}
	code, err = room.Leave(uid, servicepb.AtRoomInfo_USER_QUERY)
	if failed(ctx, name+" Leave", code, err, r) {
		return err
	}
	if err := logic.BroadcastExit(ctx, appID, roomID, uid, member); err != nil {
		ctx.Error(name+" broadcast err", zap.Error(err))
	}
	space := logic.MakeMicSpace(ctx, appID, roomID)
	code, err = space.Init()
	if failed(ctx, name+" space.Init", code, err, r) {
		return err
	}
	code, err = space.DownAll(uid, servicepb.OnMicInfo_LEAVE_ROOM)
	if failed(ctx, name+" space.DownAll", code, err, r) {
		return err
	}
	*r = success()
	return nil
}

// LeaveChatroom removes the caller from a room.
func (s *Server) LeaveChatroom(goCtx context.Context, req *servicepb.LeaveChatroomRequest) (reply *servicepb.LeaveChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.LeaveChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc LeaveChatroom", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "LeaveChatroom", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "LeaveChatroom", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "LeaveChatroom", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	err = leaveRoom(ctx, "LeaveChatroom", room, req.GetAppId(), req.GetRoomId(), req.GetUid(), &reply.Ret)
	return
}

// BatchLeaveChatroom removes the caller from several rooms; failures in
// one room do not stop the others and are not reported.
func (s *Server) BatchLeaveChatroom(goCtx context.Context, req *servicepb.BatchLeaveChatroomRequest) (reply *servicepb.BatchLeaveChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.BatchLeaveChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc BatchLeaveChatroom", zap.Int64s("roomIds", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Bool("isRoomId", req.GetIsRoomId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "BatchLeaveChatroom", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "BatchLeaveChatroom", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	appID, uid := req.GetAppId(), req.GetUid()
	roomIDs := append([]int64(nil), req.GetRoomId()...)
	if req.GetIsRoomId() {
		rooms, e := logic.GetChatroomsByAppIdUid(ctx, appID, uid)
		if e != nil {
			ctx.Error("BatchLeaveChatroom GetChatroomsByAppIdUid err", zap.Error(e))
		}
		for _, r := range rooms {
			roomIDs = append(roomIDs, r.GetChatroomId())
		}
	}

	for _, roomID := range roomIDs {
		var ignored *servicepb.UnifiedResponse
		room, e := openRoom(ctx, "BatchLeaveChatroom", appID, roomID, &ignored)
		if room == nil {
			err = e
			continue
		}
		err = leaveRoom(ctx, "BatchLeaveChatroom", room, appID, roomID, uid, &ignored)
	}
	reply.Ret = success()
	return
}

// MuteChatroom turns the room-wide mute on (action != 0) or off.
func (s *Server) MuteChatroom(goCtx context.Context, req *servicepb.MuteChatroomRequest) (reply *servicepb.MuteChatroomReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.MuteChatroomReply{Ret: serviceErr()}
	ctx.Info("rpc MuteChatroom", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int32("action", req.GetAction()), zap.Int("extLen", len(req.GetExt())))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "MuteChatroom", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "MuteChatroom", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	if !lock.AcquireRoomInfo(ctx, req.GetAppId(), req.GetRoomId(), true) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomInfo(req.GetAppId(), req.GetRoomId())

	room, err := openRoom(ctx, "MuteChatroom", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	code, err := room.SetChatroomInfoIsMuted(req.GetAction() != 0)
	if err != nil {
		ctx.Error("MuteChatroom SetChatroomInfoIsMuted err", zap.Error(err))
		return
	}
	if err = room.SaveOptExt(req.GetUid(), req.GetExt()); err != nil {
		ctx.Error("MuteChatroom SaveOptExt err", zap.Error(err))
		return
	}
	reply.Ret = ret(code)
	return
}

// EditChatroomInfo updates the room info on behalf of the business server.
func (s *Server) EditChatroomInfo(goCtx context.Context, req *servicepb.EditChatroomInfoRequest) (reply *servicepb.EditChatroomInfoReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.EditChatroomInfoReply{Ret: serviceErr()}
	info := req.GetChatRoomInfo()
	ctx.Info("rpc EditChatroomInfo", zap.Int64("roomID", req.GetRoomId()), zap.String("name", info.GetName()),
		zap.String("announcement", info.GetAnnouncement()), zap.Int64("owner", info.GetOwnerUid()),
		zap.Bool("isMuteAll", info.GetIsMuteAll()), zap.Strings("filter", info.GetCustomInfoKeyFilter()),
		zap.Any("ext", info.GetExt()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "EditChatroomInfo", &reply.Ret)

	if info == nil {
		reply.Ret = paramErr()
		return
	}
	var ok bool
	if ok, err = checkApp(ctx, "EditChatroomInfo", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	if !lock.AcquireRoomInfo(ctx, req.GetAppId(), req.GetRoomId(), true) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomInfo(req.GetAppId(), req.GetRoomId())

	room, err := logic.GetChatroom(ctx, req.GetAppId(), req.GetRoomId())
	if err != nil {
		ctx.Error("EditChatroomInfo GetChatroom err", zap.Error(err))
		return
	}
	code, err := room.ModifyChatroomInfo(info)
	if failed(ctx, "EditChatroomInfo ModifyChatroomInfo", code, err, &reply.Ret) {
		return
	}
	if open, e := room.IsOpened(); e != nil {
		err = e
		ctx.Error("EditChatroomInfo IsOpened err", zap.Error(e))
	} else if open {
		if e := logic.BroadcastInfoUpdated(ctx, req.GetAppId(), req.GetRoomId(), room.GetChatroomInfoToMap()); e != nil {
			err = e
			ctx.Error("EditChatroomInfo broadcast err", zap.Error(e))
		}
	}
	reply.Ret = success()
	return
}

// EditChatroomInfoByMember lets a member with EDIT_CHATROOM_INFO change the
// room name and announcement.
func (s *Server) EditChatroomInfoByMember(goCtx context.Context, req *servicepb.EditChatroomInfoByMemberRequest) (reply *servicepb.EditChatroomInfoByMemberReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.EditChatroomInfoByMemberReply{Ret: serviceErr()}
	ctx.Info("rpc EditChatroomInfoByMember", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.String("name", req.GetName()), zap.String("announcement", req.GetAnnouncement()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "EditChatroomInfoByMember", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "EditChatroomInfoByMember", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	if !lock.AcquireRoomInfo(ctx, req.GetAppId(), req.GetRoomId(), true) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseRoomInfo(req.GetAppId(), req.GetRoomId())

	room, err := openRoom(ctx, "EditChatroomInfoByMember", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	code, err := room.ModifyChatroomInfoByUser(req.GetUid(), req.GetName(), req.GetAnnouncement())
	if failed(ctx, "EditChatroomInfoByMember ModifyChatroomInfoByUser", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// FetchOpExt returns the extension saved by the last operation on a member.
func (s *Server) FetchOpExt(goCtx context.Context, req *servicepb.FetchOpExtRequest) (reply *servicepb.FetchOpExtReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.FetchOpExtReply{Ret: serviceErr()}
	ctx.Debug("rpc FetchOpExt", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "FetchOpExt", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "FetchOpExt", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "FetchOpExt", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	ext, code, err := room.GetOptExt(req.GetUid())
	if failed(ctx, "FetchOpExt GetOptExt", code, err, &reply.Ret) {
		return
	}
	reply.Ext = ext
	reply.Ret = success()
	return
}

// GetChatroomInfo returns the room info.
func (s *Server) GetChatroomInfo(goCtx context.Context, req *servicepb.GetChatroomInfoRequest) (reply *servicepb.GetChatroomInfoReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.GetChatroomInfoReply{Ret: serviceErr()}
	defer recoverTo(ctx, "GetChatroomInfo", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "GetChatroomInfo", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "GetChatroomInfo", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	code, info, err := room.GetChatroomInfo()
	if failed(ctx, "GetChatroomInfo", code, err, &reply.Ret) {
		return
	}
	reply.RoomInfo = info
	reply.Ret = success()
	return
}

func validRankPage(appID string, page, pageSize int32) bool {
	return page > 0 && pageSize >= 0 && appID != ""
}

// GetChatroomIdByChatroomMemberCount lists rooms ranked by member count.
func (s *Server) GetChatroomIdByChatroomMemberCount(goCtx context.Context, req *servicepb.GetChatroomIdByChatroomMemberCountRequest) (reply *servicepb.GetChatroomIdByChatroomMemberCountReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.GetChatroomIdByChatroomMemberCountReply{Ret: serviceErr()}
	if !validRankPage(req.GetAppId(), req.GetPage(), req.GetPageSize()) {
		reply.Ret = paramErr()
		return
	}
	ctx.Debug("rpc GetChatroomIdByChatroomMemberCount", zap.Bool("isDesc", req.GetIsDesc()),
		zap.Int32("page", req.GetPage()), zap.Int32("pageSize", req.GetPageSize()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "GetChatroomIdByChatroomMemberCount", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "GetChatroomIdByChatroomMemberCount", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	code, rooms, err := logic.GetSortRoomByMembers(ctx, req.GetAppId(), req.GetIsDesc(), req.GetPage(), req.GetPageSize())
	reply.RoomInfo = rooms
	if failed(ctx, "GetChatroomIdByChatroomMemberCount GetSortRoomByMembers", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// GetChatroomIdByUsedMicCount lists rooms ranked by occupied mic seats.
func (s *Server) GetChatroomIdByUsedMicCount(goCtx context.Context, req *servicepb.GetChatroomIdByUsedMicCountRequest) (reply *servicepb.GetChatroomIdByUsedMicCountReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.GetChatroomIdByUsedMicCountReply{Ret: serviceErr()}
	if !validRankPage(req.GetAppId(), req.GetPage(), req.GetPageSize()) {
		reply.Ret = paramErr()
		return
	}
	ctx.Debug("rpc GetChatroomIdByUsedMicCount", zap.Bool("isDesc", req.GetIsDesc()),
		zap.Int32("page", req.GetPage()), zap.Int32("pageSize", req.GetPageSize()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "GetChatroomIdByUsedMicCount", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "GetChatroomIdByUsedMicCount", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	code, rooms, err := logic.GetSortRoomByUsedMicCount(ctx, req.GetAppId(), req.GetIsDesc(), req.GetPage(), req.GetPageSize())
	reply.RoomInfo = rooms
	if failed(ctx, "GetChatroomIdByUsedMicCount GetSortRoomByUsedMicCount", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}
