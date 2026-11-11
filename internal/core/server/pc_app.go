package server

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/core/store"
)

// CreatePcApp 创建私聊应用
func (s *Server) CreatePcApp(ctx context.Context, req *servicepb.CreatePcAppRequest) (
	reply *servicepb.CreatePcAppReply, err error) {
	tr := startRPC("CreatePcApp", req.GetMsgTraceId(), zapcore.InfoLevel,
		zap.String("name", req.GetName()),
		zap.String("gd_app_id", req.GetGdAppId()),
		zap.String("app_key", req.GetThirdPartyAppKey()),
		zap.String("business_url", req.GetBusinessUrl()),
		zap.Bool("err_pass", req.GetErrPass()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.CreatePcAppReply{Ret: internalErrorResponse()}
	app, err := s.store.CreatePcApp(ctx, store.CreatePcAppParams{
		Name:        req.GetName(),
		Desc:        req.GetDesc(),
		GdAppID:     req.GetGdAppId(),
		AppKey:      req.GetThirdPartyAppKey(),
		AppSecret:   req.GetThirdPartyAppSecret(),
		BusinessURL: req.GetBusinessUrl(),
		ErrPass:     req.GetErrPass(),
	})
	if err != nil {
		return reply, internalError(err)
	}
	reply.Ret = successResponse()
	reply.Info = pcAppToPB(app)
	return reply, nil
}

// UpdatePcAppInfo 更新私聊应用信息
func (s *Server) UpdatePcAppInfo(ctx context.Context, req *servicepb.UpdatePcAppInfoRequest) (
	reply *servicepb.UpdatePcAppInfoReply, err error) {
	tr := startRPC("UpdatePcAppInfo", req.GetMsgTraceId(), zapcore.InfoLevel,
		zap.Uint64("id", req.GetId()),
		zap.Bool("is_refresh_secret", req.GetIsRefreshSecret()),
		zap.String("name", req.GetName()),
		zap.String("business_url", req.GetBusinessUrl()),
		zap.Bool("err_pass", req.GetErrPass()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.UpdatePcAppInfoReply{Ret: internalErrorResponse()}
	if err = s.store.UpdatePcApp(ctx, req.GetId(), store.UpdatePcAppParams{
		RefreshSecret: req.GetIsRefreshSecret(),
		ErrPass:       req.GetErrPass(),
		Name:          req.GetName(),
		Desc:          req.GetDesc(),
		BusinessURL:   req.GetBusinessUrl(),
	}); err != nil {
		return reply, internalError(err)
	}
	info, err := s.reloadPcApp(ctx, req.GetId())
	if err != nil {
		return reply, err
	}
	reply.Ret, reply.Info = pcAppReply(info)
	return reply, nil
}

// BindPcFilterApp 私聊应用绑定反垃圾应用
func (s *Server) BindPcFilterApp(ctx context.Context, req *servicepb.BindPcFilterAppRequest) (
	reply *servicepb.BindPcFilterAppReply, err error) {
	tr := startRPC("BindPcFilterApp", req.GetMsgTraceId(), zapcore.InfoLevel,
		zap.Uint64("id", req.GetId()),
		zap.String("gd_app_id", req.GetGdAppId()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.BindPcFilterAppReply{Ret: internalErrorResponse()}
	if err = s.store.BindPcFilterApp(ctx, req.GetId(), req.GetGdAppId()); err != nil {
		return reply, internalError(err)
	}
	info, err := s.reloadPcApp(ctx, req.GetId())
	if err != nil {
		return reply, err
	}
	reply.Ret, reply.Info = pcAppReply(info)
	return reply, nil
}

// QueryPcAppInfo 按主键查询私聊应用
func (s *Server) QueryPcAppInfo(ctx context.Context, req *servicepb.QueryPcAppInfoRequest) (
	reply *servicepb.QueryPcAppInfoReply, err error) {
	tr := startRPC("QueryPcAppInfo", req.GetMsgTraceId(), zapcore.DebugLevel, zap.Uint64("id", req.GetId()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.QueryPcAppInfoReply{Ret: internalErrorResponse()}
	app, err := s.store.PcAppByID(ctx, req.GetId())
	if err != nil {
		return reply, internalError(err)
	}
	reply.Ret, reply.Info = pcAppReply(app)
	return reply, nil
}

// QueryPcAppInfoByAppID 按 app id 查询私聊应用
func (s *Server) QueryPcAppInfoByAppID(ctx context.Context, req *servicepb.QueryPcAppInfoByAppIDRequest) (
	reply *servicepb.QueryPcAppInfoReply, err error) {
	tr := startRPC("QueryPcAppInfoByAppID", req.GetMsgTraceId(), zapcore.DebugLevel, zap.String("app_id", req.GetAppId()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.QueryPcAppInfoReply{}
	reply.Ret, reply.Info = pcAppReply(s.apps.PcApp(ctx, req.GetAppId()))
	return reply, nil
}

// reloadPcApp reads the app after a write and drops its cached copy.
func (s *Server) reloadPcApp(ctx context.Context, id uint64) (*store.PcAppInfo, error) {
	app, err := s.store.PcAppByID(ctx, id)
	if err != nil {
		return nil, internalError(err)
	}
	if app != nil {
		s.apps.InvalidatePcApp(app.AppId)
	}
	return app, nil
}

func pcAppReply(app *store.PcAppInfo) (*servicepb.UnifiedResponse, *servicepb.PcAppInfo) {
	if app == nil {
		return notExistsResponse(), nil
	}
	return successResponse(), pcAppToPB(app)
}
