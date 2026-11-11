package server

import (
	"context"
	"strconv"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/core/store"
)

// defaultCopyTypes enables message copy for every message type.
var defaultCopyTypes = strconv.Itoa(int(servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_ALL))

// IsAppValid 应用是否已注册
func (s *Server) IsAppValid(ctx context.Context, req *servicepb.IsAppValidRequest) (
	reply *servicepb.IsAppValidReply, err error) {
	tr := startRPC("IsAppValid", req.GetMsgTraceId(), zapcore.DebugLevel, zap.String("app_id", req.GetAppId()))
	defer func() { tr.finish(reply, err) }()

	return &servicepb.IsAppValidReply{
		Ret:     successResponse(),
		IsValid: s.apps.IsAppValid(ctx, req.GetAppId()),
	}, nil
}

// CreateApp 创建应用
func (s *Server) CreateApp(ctx context.Context, req *servicepb.CreateAppRequest) (
	reply *servicepb.CreateAppReply, err error) {
	tr := startRPC("CreateApp", req.GetMsgTraceId(), zapcore.InfoLevel,
		zap.String("name", req.GetName()),
		zap.String("msg_cp_url", req.GetMsgCpUrl()),
		zap.String("types", req.GetTypes()),
		zap.Int64("qps", req.GetQps()),
		zap.String("media_app_id", req.GetMediaAppId()),
		zap.String("gd_app_id", req.GetGdAppId()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.CreateAppReply{Ret: internalErrorResponse()}
	types := req.GetTypes()
	if types == "" {
		types = defaultCopyTypes
	}
	app, err := s.store.CreateApp(ctx, store.CreateAppParams{
		Name:           req.GetName(),
		Desc:           req.GetDesc(),
		MsgCpURL:       req.GetMsgCpUrl(),
		Types:          types,
		MediaAppID:     req.GetMediaAppId(),
		MediaAppSecret: req.GetMediaAppSecret(),
		GdAppID:        req.GetGdAppId(),
		QPS:            req.GetQps(),
	})
	if err != nil {
		return reply, internalError(err)
	}
	reply.Ret = successResponse()
	reply.Info = appToPB(app)

	s.notifier.AppInfoUpdated(ctx, appUpdatedRequest(app, req.GetMsgTraceId()))
	return reply, nil
}

// QueryAppInfo 查询应用信息
func (s *Server) QueryAppInfo(ctx context.Context, req *servicepb.QueryAppInfoRequest) (
	reply *servicepb.QueryAppInfoReply, err error) {
	tr := startRPC("QueryAppInfo", req.GetMsgTraceId(), zapcore.DebugLevel, zap.Uint64("id", req.GetId()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.QueryAppInfoReply{Ret: internalErrorResponse()}
	app, err := s.store.AppByID(ctx, req.GetId())
	if err != nil {
		return reply, internalError(err)
	}
	if app == nil {
		reply.Ret = notExistsResponse()
		return reply, nil
	}
	reply.Ret = successResponse()
	reply.Info = appToPB(app)
	return reply, nil
}

// UpdateAppInfo 更新应用信息，并通知 webhook 与其他 core 实例刷新缓存
func (s *Server) UpdateAppInfo(ctx context.Context, req *servicepb.UpdateAppInfoRequest) (
	reply *servicepb.UpdateAppInfoReply, err error) {
	tr := startRPC("UpdateAppInfo", req.GetMsgTraceId(), zapcore.InfoLevel,
		zap.Uint64("id", req.GetId()),
		zap.Bool("is_refresh_secret", req.GetIsRefreshSecret()),
		zap.String("name", req.GetName()),
		zap.String("msg_cp_url", req.GetMsgCpUrl()),
		zap.String("text_cp_url", req.GetTextCpUrl()),
		zap.String("custom_cp_url", req.GetCustomCpUrl()),
		zap.String("types", req.GetTypes()),
		zap.Int64("qps", req.GetQps()),
		zap.Int64("text_qps", req.GetTextQps()),
		zap.Int64("custom_qps", req.GetCustomQps()),
		zap.String("media_app_id", req.GetMediaAppId()),
		zap.String("gd_app_id", req.GetGdAppId()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.UpdateAppInfoReply{Ret: internalErrorResponse()}
	if err = s.store.UpdateApp(ctx, req.GetId(), store.UpdateAppParams{
		RefreshSecret:  req.GetIsRefreshSecret(),
		Name:           req.GetName(),
		Desc:           req.GetDesc(),
		MsgCpURL:       req.GetMsgCpUrl(),
		TextCpURL:      req.GetTextCpUrl(),
		CustomCpURL:    req.GetCustomCpUrl(),
		Types:          req.GetTypes(),
		MediaAppID:     req.GetMediaAppId(),
		MediaAppSecret: req.GetMediaAppSecret(),
		GdAppID:        req.GetGdAppId(),
		QPS:            req.GetQps(),
		TextQPS:        req.GetTextQps(),
		CustomQPS:      req.GetCustomQps(),
	}); err != nil {
		return reply, internalError(err)
	}

	app, err := s.store.AppByID(ctx, req.GetId())
	if err != nil {
		return reply, internalError(err)
	}
	if app == nil {
		reply.Ret = notExistsResponse()
		return reply, nil
	}
	reply.Ret = successResponse()
	reply.Info = appToPB(app)

	s.apps.InvalidateApp(app.AppId)
	s.notifier.AppInfoUpdated(ctx, appUpdatedRequest(app, req.GetMsgTraceId()))
	s.notifier.AppCacheInvalidated(ctx, app.AppId, req.GetMsgTraceId())
	return reply, nil
}

// GetAppSecret 获取应用密钥与抄送配置
func (s *Server) GetAppSecret(ctx context.Context, req *servicepb.GetAppSecretRequest) (
	reply *servicepb.GetAppSecretReply, err error) {
	tr := startRPC("GetAppSecret", req.GetMsgTraceId(), zapcore.DebugLevel, zap.String("app_id", req.GetAppId()))
	defer func() { tr.finish(reply, err) }()

	app := s.apps.App(ctx, req.GetAppId())
	if app == nil {
		return &servicepb.GetAppSecretReply{Ret: notExistsResponse()}, nil
	}
	return &servicepb.GetAppSecretReply{
		Ret:            successResponse(),
		Secret:         app.AppSecret,
		MsgCpUrl:       app.MsgCpUrl,
		TextCpUrl:      app.TextCpUrl,
		CustomCpUrl:    app.CustomCpUrl,
		Types:          app.Types,
		Qps:            app.Qps,
		TextQps:        app.TextQps,
		CustomQps:      app.CustomQps,
		MediaAppId:     app.MediaAppId,
		MediaAppSecret: app.MediaAppSecret,
		GdAppId:        app.GdAppId,
	}, nil
}

// GetAllAppMsgCpQPS 获取所有应用抄送 QPS 限制
func (s *Server) GetAllAppMsgCpQPS(ctx context.Context, req *servicepb.GetAllAppMsgCpQPSRequest) (
	reply *servicepb.GetAllAppMsgCpQPSReply, err error) {
	tr := startRPC("GetAllAppMsgCpQPS", req.GetMsgTraceId(), zapcore.DebugLevel)
	defer func() { tr.finish(reply, err) }()

	qps := s.apps.AllAppQPS(ctx)
	if len(qps) == 0 {
		return &servicepb.GetAllAppMsgCpQPSReply{Ret: notExistsResponse()}, nil
	}
	return &servicepb.GetAllAppMsgCpQPSReply{Ret: successResponse(), Qps: qps}, nil
}

// UpdateAppCacheInfo 丢弃本实例的应用缓存
func (s *Server) UpdateAppCacheInfo(_ context.Context, req *servicepb.UpdateAppCacheInfoRequest) (
	reply *servicepb.UpdateAppCacheInfoReply, err error) {
	tr := startRPC("UpdateAppCacheInfo", req.GetMsgTraceId(), zapcore.DebugLevel, zap.String("app_id", req.GetAppId()))
	defer func() { tr.finish(reply, err) }()

	s.apps.InvalidateApp(req.GetAppId())
	return &servicepb.UpdateAppCacheInfoReply{Ret: successResponse()}, nil
}
