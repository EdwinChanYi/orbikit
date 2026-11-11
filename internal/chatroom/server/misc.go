package server

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/lock"
	"github.com/yourname/roomkit/internal/chatroom/logic"
)

// UpdateAppCustomInfoKeyFilter only validates the app; per-app filters are
// not stored and the reply keeps the default CHATROOM_SERVICE_ERROR code.
func (s *Server) UpdateAppCustomInfoKeyFilter(goCtx context.Context, req *servicepb.UpdateAppCustomInfoKeyFilterRequest) (reply *servicepb.UpdateAppCustomInfoKeyFilterReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UpdateAppCustomInfoKeyFilterReply{Ret: serviceErr()}
	ctx.Debug("rpc UpdateAppCustomInfoKeyFilter")
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "UpdateAppCustomInfoKeyFilter", &reply.Ret)

	_, err = checkApp(ctx, "UpdateAppCustomInfoKeyFilter", req.GetAppId(), &reply.Ret)
	return
}

// UserOnline records that the user connected through a gateway.
func (s *Server) UserOnline(goCtx context.Context, req *servicepb.UserOnlineRequest) (reply *servicepb.UserOnlineReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UserOnlineReply{Ret: serviceErr()}
	defer recoverTo(ctx, "UserOnline", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "UserOnline", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	if !lock.AcquireUser(ctx, req.GetAppId(), 0, req.GetUid(), true) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseUser(req.GetAppId(), 0, req.GetUid())

	if err = logic.Online(ctx, req.GetAppId(), req.GetUid(), req.GetConnUuid()); err != nil {
		ctx.Error("UserOnline Online err", zap.Error(err))
		return
	}
	reply.Ret = success()
	return
}

// UserOffline records that the user's connection closed. The user leaves
// all rooms when they do not reconnect within the configured grace period.
func (s *Server) UserOffline(goCtx context.Context, req *servicepb.UserOfflineRequest) (reply *servicepb.UserOfflineReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UserOfflineReply{Ret: serviceErr()}
	defer recoverTo(ctx, "UserOffline", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "UserOffline", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	if !lock.AcquireUser(ctx, req.GetAppId(), 0, req.GetUid(), true) {
		reply.Ret = ret(servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED)
		return
	}
	defer lock.ReleaseUser(req.GetAppId(), 0, req.GetUid())

	if err = logic.Offline(ctx, req.GetAppId(), req.GetUid(), req.GetConnUuid()); err != nil {
		ctx.Error("UserOffline Offline err", zap.Error(err))
		return
	}
	reply.Ret = success()
	return
}

// RestraintUserOper bans (action 1) or unbans a user through the core
// service; a ban also disconnects the user.
func (s *Server) RestraintUserOper(goCtx context.Context, req *servicepb.RestraintUserOperRequest) (reply *servicepb.RestraintUserOperReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.RestraintUserOperReply{Ret: serviceErr()}
	ctx.Debug("rpc RestraintUserOper", zap.Int64("uid", req.GetUid()), zap.Int32("action", req.GetAction()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "RestraintUserOper", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "RestraintUserOper", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	code, err := logic.RestrainUser(ctx, req.GetAppId(), req.GetUid(), req.GetAction())
	if failed(ctx, "RestraintUserOper RestrainUser", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

var errLogUploadUnsupported = status.Error(codes.Unimplemented, "client log upload requires an object storage backend, which is not configured")

// UploadClientLog is not supported: it depended on a third-party object
// storage service that is not part of this deployment.
func (s *Server) UploadClientLog(context.Context, *servicepb.UploadClientLogRequest) (*servicepb.UploadClientLogReply, error) {
	return nil, errLogUploadUnsupported
}

// UploadClientLogStatus is not supported; see UploadClientLog.
func (s *Server) UploadClientLogStatus(context.Context, *servicepb.UploadClientLogStatusRequest) (*servicepb.UploadClientLogStatusReply, error) {
	return nil, errLogUploadUnsupported
}

// UserMicTime pages through a user's mic sessions.
func (s *Server) UserMicTime(goCtx context.Context, req *servicepb.UserMicTimeRequest) (reply *servicepb.UserMicTimeReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UserMicTimeReply{Ret: success()}
	sort := req.GetSortType()
	if sort != servicepb.UserMicTimeRequest_DESC && sort != servicepb.UserMicTimeRequest_ASC {
		reply.Ret = paramErr()
		return
	}
	ctx.Debug("rpc UserMicTime", zap.Int64("uid", req.GetUid()), zap.String("sortType", sort.String()),
		zap.Int32("pageSize", req.GetPageSize()), zap.Int64("lastUpTime", req.GetLastUpTime()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "UserMicTime", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "UserMicTime", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	infos, code, err := logic.QueryUserAtMicDuration(req.GetAppId(), req.GetUid(), req.GetLastUpTime(), sort, req.GetPageSize())
	reply.MicInfo = infos
	if failed(ctx, "UserMicTime QueryUserAtMicDuration", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// UserAtRoomTime pages through a user's room sessions.
func (s *Server) UserAtRoomTime(goCtx context.Context, req *servicepb.UserAtRoomTimeRequest) (reply *servicepb.UserAtRoomTimeReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.UserAtRoomTimeReply{Ret: success()}
	sort := req.GetSortType()
	if sort != servicepb.UserAtRoomTimeRequest_DESC && sort != servicepb.UserAtRoomTimeRequest_ASC {
		reply.Ret = paramErr()
		return
	}
	ctx.Debug("rpc UserAtRoomTime", zap.Int64("uid", req.GetUid()), zap.String("sortType", sort.String()),
		zap.Int32("pageSize", req.GetPageSize()), zap.Int64("lastEnterTime", req.GetLastEnterTime()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "UserAtRoomTime", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "UserAtRoomTime", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	infos, code, err := logic.QueryUserAtRoomDuration(req.GetAppId(), req.GetUid(), req.GetLastEnterTime(), sort, req.GetPageSize())
	reply.RoomInfo = infos
	if failed(ctx, "UserAtRoomTime QueryUserAtRoomDuration", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}
