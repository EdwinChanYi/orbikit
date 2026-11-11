package server

import (
	"context"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/logic"
)

// SetRole replaces all roles of a member with the given list.
func (s *Server) SetRole(goCtx context.Context, req *servicepb.SetRoleRequest) (reply *servicepb.SetRoleReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.SetRoleReply{Ret: serviceErr()}
	if len(req.GetRoleId()) == 0 {
		reply.Ret = paramErr()
		return
	}
	ctx.Info("rpc SetRole", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int64s("roleIDs", req.GetRoleId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "SetRole", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "SetRole", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "SetRole", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	open, err := room.IsOpened()
	if err != nil {
		ctx.Error("SetRole IsOpened err", zap.Error(err))
	}
	code, err := room.SetChatRoomUserRole(req.GetUid(), req.GetRoleId(), open)
	if failed(ctx, "SetRole SetChatRoomUserRole", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// RemoveUserRole removes one role from a member.
func (s *Server) RemoveUserRole(goCtx context.Context, req *servicepb.RemoveUserRoleRequest) (reply *servicepb.RemoveUserRoleReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.RemoveUserRoleReply{Ret: serviceErr()}
	ctx.Info("rpc RemoveUserRole", zap.Int64("roomID", req.GetRoomId()), zap.Int64("uid", req.GetUid()),
		zap.Int64("roleID", req.GetRoleId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "RemoveUserRole", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "RemoveUserRole", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	room, err := openRoom(ctx, "RemoveUserRole", req.GetAppId(), req.GetRoomId(), &reply.Ret)
	if room == nil {
		return
	}
	open, err := room.IsOpened()
	if err != nil {
		ctx.Error("RemoveUserRole IsOpened err", zap.Error(err))
	}
	code, err := room.RemoveChatRoomUserRole(req.GetUid(), req.GetRoleId(), open)
	if failed(ctx, "RemoveUserRole RemoveChatRoomUserRole", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// CreateRole defines a role and its permissions for an app.
func (s *Server) CreateRole(goCtx context.Context, req *servicepb.CreateRoleRequest) (reply *servicepb.CreateRoleReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.CreateRoleReply{Ret: serviceErr()}
	ctx.Debug("rpc CreateRole", zap.String("name", req.GetName()), zap.Int64("roleID", req.GetRoleId()),
		zap.Any("permissions", req.GetPermissions()), zap.Bool("keepOnClosed", req.GetKeepOnClosed()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "CreateRole", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "CreateRole", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	code, err := logic.CreateRole(req.GetAppId(), req.GetName(), req.GetRoleId(), req.GetPermissions(), req.GetKeepOnClosed())
	if failed(ctx, "CreateRole", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// QueryRole returns the given roles of an app, or all of them when RoleId is empty.
func (s *Server) QueryRole(goCtx context.Context, req *servicepb.QueryRoleRequest) (reply *servicepb.QueryRoleReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.QueryRoleReply{Ret: serviceErr()}
	ctx.Debug("rpc QueryRole", zap.Int64s("roleIDs", req.GetRoleId()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "QueryRole", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "QueryRole", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	roles, code, err := logic.QueryRole(req.GetAppId(), req.GetRoleId())
	reply.Roles = roles
	if failed(ctx, "QueryRole", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}

// ModifyRole replaces the permissions of a role.
func (s *Server) ModifyRole(goCtx context.Context, req *servicepb.ModifyRoleRequest) (reply *servicepb.ModifyRoleReply, err error) {
	ctx := newCtx(goCtx, req.GetMsgTraceId(), req.GetAppId())
	reply = &servicepb.ModifyRoleReply{Ret: serviceErr()}
	ctx.Debug("rpc ModifyRole", zap.Int64("roleID", req.GetRoleId()), zap.Any("permissions", req.GetPermissions()),
		zap.Bool("keepOnClosed", req.GetKeepOnClosed()))
	defer func() { ctx.TraceReply(reply, err) }()
	defer recoverTo(ctx, "ModifyRole", &reply.Ret)

	var ok bool
	if ok, err = checkApp(ctx, "ModifyRole", req.GetAppId(), &reply.Ret); !ok {
		return
	}
	code, err := logic.ModifyRole(req.GetAppId(), req.GetRoleId(), req.GetPermissions(), req.GetKeepOnClosed())
	if failed(ctx, "ModifyRole", code, err, &reply.Ret) {
		return
	}
	reply.Ret = success()
	return
}
