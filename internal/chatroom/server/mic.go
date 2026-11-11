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

const slowMicLock = 100 * time.Millisecond

// micRequest is the part shared by the mic operation handlers.
type micRequest struct {
	name     string
	appID    string
	roomID   int64
	operator int64
}

// prepareMic checks the app, takes the room lock, loads the open room and,
// when the operator is a user (non-zero uid), checks that they are in it.
// On success the caller must call the returned release function.
func prepareMic(ctx *reqctx.Context, m micRequest, r **servicepb.UnifiedResponse) (*logic.ChatroomContainer, func(), error) {
	if ok, err := checkApp(ctx, m.name, m.appID, r); !ok {
		return nil, nil, err
	}
	if !lock.AcquireRoomProcess(ctx, m.appID, m.roomID) {
		*r = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return nil, nil, nil
	}
	release := func() { lock.ReleaseRoomProcess(m.appID, m.roomID) }
	room, err := openRoom(ctx, m.name, m.appID, m.roomID, r)
	if room == nil {
		release()
		return nil, nil, err
	}
	if m.operator != 0 {
		code, err := room.IsUserInChatroom(m.operator)
		if failed(ctx, m.name+" IsUserInChatroom", code, err, r) {
			release()
			return nil, nil, err
		}
	}
	return room, release, nil
}

func initSpace(ctx *reqctx.Context, name, appID string, roomID int64, r **servicepb.UnifiedResponse) (*logic.MicSpace, error) {
	space := logic.MakeMicSpace(ctx, appID, roomID)
	code, err := space.Init()
	if failed(ctx, name+" space.Init", code, err, r) {
		return nil, err
	}
	return space, nil
}

// BlockMic locks (action != 0) or unlocks a seat and returns the layout.
func (s *Server) BlockMic(goCtx context.Context, req *servicepb.BlockMicRequest) (reply *servicepb.BlockMicReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.BlockMicReply{Ret: serviceErr()}
	ctx.Debug("rpc BlockMic", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int32("micIndex", req.GetMicIndex()), zap.Int32("action", req.GetAction()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "BlockMic", &reply.Ret)

	room, release, err := prepareMic(ctx, micRequest{"BlockMic", req.GetAppId(), req.GetRoomId(), req.GetUid()}, &reply.Ret)
	if room == nil {
		return
	}
	defer release()
	space, err := initSpace(ctx, "BlockMic", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if space == nil {
		return
	}
	code, err := space.Block(req.GetUid(), req.GetMicIndex(), req.GetAction())
	if failed(ctx, "BlockMic space.Block", code, err, &reply.Ret) {
		return
	}
	infos, code, err := micInfos(room, space)
	if failed(ctx, "BlockMic micInfos", code, err, &reply.Ret) {
		return
	}
	reply.MicInfo = infos
	reply.Ret = success()
	return
}

// MuteMic mutes (action != 0) or unmutes a seat and returns the layout.
func (s *Server) MuteMic(goCtx context.Context, req *servicepb.MuteMicRequest) (reply *servicepb.MuteMicReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.MuteMicReply{Ret: serviceErr()}
	ctx.Debug("rpc MuteMic", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int32("micIndex", req.GetMicIndex()), zap.Int32("action", req.GetAction()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "MuteMic", &reply.Ret)

	room, release, err := prepareMic(ctx, micRequest{"MuteMic", req.GetAppId(), req.GetRoomId(), req.GetUid()}, &reply.Ret)
	if room == nil {
		return
	}
	defer release()
	space, err := initSpace(ctx, "MuteMic", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if space == nil {
		return
	}
	code, err := space.Mute(req.GetUid(), req.GetMicIndex(), req.GetAction())
	if failed(ctx, "MuteMic space.Mute", code, err, &reply.Ret) {
		return
	}
	infos, code, err := micInfos(room, space)
	if failed(ctx, "MuteMic micInfos", code, err, &reply.Ret) {
		return
	}
	reply.MicInfo = infos
	reply.Ret = success()
	return
}

// QueryMicInfo returns the mic layout of a room. A room without seats keeps
// the default CHATROOM_SERVICE_ERROR code.
func (s *Server) QueryMicInfo(goCtx context.Context, req *servicepb.QueryMicInfoRequest) (reply *servicepb.QueryMicInfoReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.QueryMicInfoReply{Ret: serviceErr()}
	defer recoverTo(ctx, "QueryMicInfo", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "QueryMicInfo", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "QueryMicInfo", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	if req.GetUid() != 0 {
		code, e := room.IsUserInChatroom(req.GetUid())
		if failed(ctx, "QueryMicInfo IsUserInChatroom", code, e, &reply.Ret) {
			return reply, e
		}
	}
	space, err := initSpace(ctx, "QueryMicInfo", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if space == nil {
		return
	}
	infos, code, err := micInfos(room, space)
	if failed(ctx, "QueryMicInfo micInfos", code, err, &reply.Ret) {
		return
	}
	if len(infos) > 0 {
		reply.MicInfo = infos
		reply.Ret = success()
	}
	return
}

// UpMic puts the caller on a seat.
func (s *Server) UpMic(goCtx context.Context, req *servicepb.UpMicRequest) (reply *servicepb.UpMicReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UpMicReply{Ret: serviceErr()}
	ctx.Debug("rpc UpMic", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int32("micIndex", req.GetMicIndex()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "UpMic", &reply.Ret)

	room, release, err := prepareMic(ctx, micRequest{"UpMic", req.GetAppId(), req.GetRoomId(), 0}, &reply.Ret)
	if room == nil {
		return
	}
	defer release()
	code, err := room.IsUserInChatroom(req.GetUid())
	if failed(ctx, "UpMic IsUserInChatroom", code, err, &reply.Ret) {
		return
	}
	space, err := initSpace(ctx, "UpMic", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if space == nil {
		return
	}
	if code, err = space.Up(req.GetUid(), req.GetMicIndex()); err != nil {
		ctx.Error("UpMic space.Up err", zap.Error(err))
		return
	}
	reply.Ret = ret(code)
	return
}

// DownMic takes a user off a seat.
func (s *Server) DownMic(goCtx context.Context, req *servicepb.DownMicRequest) (reply *servicepb.DownMicReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.DownMicReply{Ret: serviceErr()}
	ctx.Debug("rpc DownMic", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int32("micIndex", req.GetMicIndex()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "DownMic", &reply.Ret)

	room, release, err := prepareMic(ctx, micRequest{"DownMic", req.GetAppId(), req.GetRoomId(), 0}, &reply.Ret)
	if room == nil {
		return
	}
	defer release()
	space, err := initSpace(ctx, "DownMic", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if space == nil {
		return
	}
	code, err := space.Down(req.GetUid(), req.GetMicIndex(), servicepb.OnMicInfo_USER_QUERY)
	if err != nil {
		ctx.Error("DownMic space.Down err", zap.Error(err))
		return
	}
	reply.Ret = ret(code)
	return
}

// InviteMic puts another member on a seat.
func (s *Server) InviteMic(goCtx context.Context, req *servicepb.InviteMicRequest) (reply *servicepb.InviteMicReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.InviteMicReply{Ret: serviceErr()}
	ctx.Debug("rpc InviteMic", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int64("inviteUid", req.GetInviteUid()), zap.Int32("micIndex", req.GetMicIndex()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "InviteMic", &reply.Ret)

	room, release, err := prepareMic(ctx, micRequest{"InviteMic", req.GetAppId(), req.GetRoomId(), req.GetUid()}, &reply.Ret)
	if room == nil {
		return
	}
	defer release()
	code, err := room.IsUserInChatroom(req.GetInviteUid())
	if failed(ctx, "InviteMic IsUserInChatroom invitee", code, err, &reply.Ret) {
		return
	}
	space, err := initSpace(ctx, "InviteMic", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if space == nil {
		return
	}
	if code, err = space.Invite(req.GetUid(), req.GetInviteUid(), req.GetMicIndex()); err != nil {
		ctx.Error("InviteMic space.Invite err", zap.Error(err))
		return
	}
	reply.Ret = ret(code)
	return
}

// KickMic takes another member off a seat.
func (s *Server) KickMic(goCtx context.Context, req *servicepb.KickMicRequest) (reply *servicepb.KickMicMicReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.KickMicMicReply{Ret: serviceErr()}
	ctx.Debug("rpc KickMic", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int64("kickUid", req.GetKicUid()), zap.Int32("micIndex", req.GetMicIndex()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "KickMic", &reply.Ret)

	room, release, err := prepareMic(ctx, micRequest{"KickMic", req.GetAppId(), req.GetRoomId(), req.GetUid()}, &reply.Ret)
	if room == nil {
		return
	}
	defer release()
	space, err := initSpace(ctx, "KickMic", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if space == nil {
		return
	}
	code, err := space.Kick(req.GetUid(), req.GetKicUid(), req.GetMicIndex())
	if err != nil {
		ctx.Error("KickMic space.Kick err", zap.Error(err))
		return
	}
	reply.Ret = ret(code)
	return
}

// UpdateMicSpace replaces the mic layout of a room.
func (s *Server) UpdateMicSpace(goCtx context.Context, req *servicepb.UpdateMicSpaceRequest) (reply *servicepb.UpdateMicSpaceReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UpdateMicSpaceReply{Ret: serviceErr()}
	ctx.Debug("rpc UpdateMicSpace", zap.Int64("roomID", req.GetRoomId()), zap.Int("space", len(req.GetSpace())))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "UpdateMicSpace", &reply.Ret)

	appID, roomID := req.GetAppId(), req.GetRoomId()
	room, release, err := prepareMic(ctx, micRequest{"UpdateMicSpace", appID, roomID, 0}, &reply.Ret)
	if room == nil {
		return
	}
	defer release()

	space := logic.MakeMicSpace(ctx, appID, roomID)
	si := &logic.MicSpaceInfo{AppId: appID, ChatroomId: roomID, ElementInfo: seatsFromSpace(req.GetSpace())}
	isLess, code, err := space.CreateOrUpdate(si)
	if failed(ctx, "UpdateMicSpace CreateOrUpdate", code, err, &reply.Ret) {
		return
	}
	if code, err = space.Init(); failed(ctx, "UpdateMicSpace space.Init", code, err, &reply.Ret) {
		return
	}
	layout := space.GetMicInfo(0, 0, 0)
	if isLess {
		if e := logic.BroadcastMicChange(ctx, appID, roomID, layout); e != nil {
			ctx.Error("UpdateMicSpace mic change broadcast err", zap.Error(e))
		}
	}
	if err = logic.BroadcastMicStatus(ctx, appID, roomID, layout); err != nil {
		ctx.Error("UpdateMicSpace mic status broadcast err", zap.Error(err))
	}
	reply.Ret = success()
	return
}

// UpdateMicSpaceExt updates the extension fields of seats.
func (s *Server) UpdateMicSpaceExt(goCtx context.Context, req *servicepb.UpdateMicSpaceExtRequest) (reply *servicepb.UpdateMicSpaceExtReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UpdateMicSpaceExtReply{Ret: serviceErr()}
	ctx.Info("rpc UpdateMicSpaceExt", zap.Int64("roomID", req.GetRoomId()), zap.Int("spaceExt", len(req.GetSpacesExt())))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "UpdateMicSpaceExt", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "UpdateMicSpaceExt", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	appID, roomID := req.GetAppId(), req.GetRoomId()
	start := time.Now()
	if !lock.AcquireMic(ctx, appID, roomID) {
		ctx.Error("UpdateMicSpaceExt get mic lock fail", zap.Int64("roomID", roomID), zap.Duration("cost", time.Since(start)))
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseMic(appID, roomID)
	if cost := time.Since(start); cost > slowMicLock {
		ctx.Info("UpdateMicSpaceExt slow mic lock", zap.Int64("roomID", roomID), zap.Duration("cost", cost))
	}

	room, err := openRoom(ctx, "UpdateMicSpaceExt", appID, roomID, &reply.Ret)
	if room == nil {
		return
	}
	space := logic.MakeMicSpace(ctx, appID, roomID)
	code, err := space.CreateOrUpdateExt(req)
	if failed(ctx, "UpdateMicSpaceExt CreateOrUpdateExt", code, err, &reply.Ret) {
		return
	}
	if code, err = space.Init(); failed(ctx, "UpdateMicSpaceExt space.Init", code, err, &reply.Ret) {
		return
	}
	if err = logic.BroadcastMicStatus(ctx, appID, roomID, space.GetMicInfo(0, 0, 0)); err != nil {
		ctx.Error("UpdateMicSpaceExt mic status broadcast err", zap.Error(err))
	}
	reply.Ret = success()
	return
}

// UpdateMicToken stores the media token of a user's seat.
func (s *Server) UpdateMicToken(goCtx context.Context, req *servicepb.UpdateMicTokenRequest) (reply *servicepb.UpdateMicTokenReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UpdateMicTokenReply{Ret: serviceErr()}
	ctx.Info("rpc UpdateMicToken", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "UpdateMicToken", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "UpdateMicToken", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	code, err := logic.UpdateMicToken(ctx, req.GetAppId(), req.GetToken(), req.GetRoomId(), req.GetUid())
	if failed(ctx, "UpdateMicToken", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}
