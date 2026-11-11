package server

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/core/store"
)

type gdAppRequest interface {
	GetName() string
	GetDesc() string
	GetTypes() string
	GetGdAppId() string
	GetNoStrictTypes() string
	GetStarTypes1() string
	GetStarTypes2() string
	GetBusinessModerationCheckUrl() string
	GetMatchLevel() uint32
	GetMsgLenMax() int32
	GetMsgDigitalCountMax() int32
	GetDupliTextCount() int32
	GetTextCountExpire() int32
	GetNeedMatchBase() bool
	GetMarkStar() bool
	GetMarkStar1() bool
	GetMarkStar2() bool
	GetIsBlackDupliUser() bool
	GetIsAddWordStore() bool
}

func gdAppParams(req gdAppRequest) store.GdAppParams {
	dupli, expire := normalizeDupli(req.GetDupliTextCount(), req.GetTextCountExpire())
	return store.GdAppParams{
		Name:                       req.GetName(),
		Desc:                       req.GetDesc(),
		Types:                      req.GetTypes(),
		GdAppID:                    req.GetGdAppId(),
		NoStrictTypes:              req.GetNoStrictTypes(),
		StarTypes1:                 req.GetStarTypes1(),
		StarTypes2:                 req.GetStarTypes2(),
		BusinessModerationCheckUrl: req.GetBusinessModerationCheckUrl(),
		MatchLevel:                 req.GetMatchLevel(),
		MsgLenMax:                  req.GetMsgLenMax(),
		MsgDigitalCountMax:         req.GetMsgDigitalCountMax(),
		DupliTextCount:             dupli,
		TextCountExpire:            expire,
		NeedMatchBase:              req.GetNeedMatchBase(),
		MarkStar:                   req.GetMarkStar(),
		MarkStar1:                  req.GetMarkStar1(),
		MarkStar2:                  req.GetMarkStar2(),
		IsBlackDupliUser:           req.GetIsBlackDupliUser(),
		IsAddWordStore:             req.GetIsAddWordStore(),
	}
}

func gdAppLogFields(req gdAppRequest) []zap.Field {
	return []zap.Field{
		zap.String("name", req.GetName()),
		zap.Uint32("match_level", req.GetMatchLevel()),
		zap.String("no_strict_types", req.GetNoStrictTypes()),
		zap.Bool("need_match_base", req.GetNeedMatchBase()),
		zap.Bool("mark_star", req.GetMarkStar()),
		zap.Bool("mark_star1", req.GetMarkStar1()),
		zap.String("star_types1", req.GetStarTypes1()),
		zap.Bool("mark_star2", req.GetMarkStar2()),
		zap.String("star_types2", req.GetStarTypes2()),
		zap.Int32("msg_len_max", req.GetMsgLenMax()),
		zap.Int32("msg_digital_count_max", req.GetMsgDigitalCountMax()),
		zap.Int32("dupli_text_count", req.GetDupliTextCount()),
		zap.Int32("text_count_expire", req.GetTextCountExpire()),
	}
}

// CreateGdApp 创建反垃圾应用配置
func (s *Server) CreateGdApp(ctx context.Context, req *servicepb.CreateGdAppRequest) (
	reply *servicepb.CreateGdAppReply, err error) {
	tr := startRPC("CreateGdApp", req.GetMsgTraceId(), zapcore.InfoLevel, gdAppLogFields(req)...)
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.CreateGdAppReply{Ret: internalErrorResponse()}
	app, err := s.store.CreateGdApp(ctx, gdAppParams(req))
	if err != nil {
		return reply, internalError(err)
	}
	reply.Ret = successResponse()
	reply.Info = gdAppToPB(app)
	return reply, nil
}

// UpdateGdAppInfo 更新反垃圾应用配置
func (s *Server) UpdateGdAppInfo(ctx context.Context, req *servicepb.UpdateGdAppInfoRequest) (
	reply *servicepb.UpdateGdAppInfoReply, err error) {
	tr := startRPC("UpdateGdAppInfo", req.GetMsgTraceId(), zapcore.InfoLevel,
		append(gdAppLogFields(req), zap.Uint64("id", req.GetId()))...)
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.UpdateGdAppInfoReply{Ret: internalErrorResponse()}
	if err = s.store.UpdateGdApp(ctx, req.GetId(), gdAppParams(req)); err != nil {
		return reply, internalError(err)
	}
	app, err := s.store.GdAppByID(ctx, req.GetId())
	if err != nil {
		return reply, internalError(err)
	}
	if app == nil {
		reply.Ret = notExistsResponse()
		return reply, nil
	}
	s.apps.InvalidateGdApp(app.AppId)
	reply.Ret = successResponse()
	reply.Info = gdAppToPB(app)
	return reply, nil
}

// GetGdAppSecret 根据反垃圾 app id 获取反垃圾应用配置
func (s *Server) GetGdAppSecret(ctx context.Context, req *servicepb.GetGdAppSecretRequest) (
	reply *servicepb.GetGdAppSecretReply, err error) {
	tr := startRPC("GetGdAppSecret", req.GetMsgTraceId(), zapcore.DebugLevel, zap.String("app_id", req.GetAppId()))
	defer func() { tr.finish(reply, err) }()

	app := s.apps.GdApp(ctx, req.GetAppId())
	if app == nil {
		return &servicepb.GetGdAppSecretReply{Ret: notExistsResponse()}, nil
	}
	return &servicepb.GetGdAppSecretReply{
		Ret:                        successResponse(),
		Secret:                     app.AppSecret,
		MatchLevel:                 app.MatchLevel,
		NoStrictTypes:              app.NoStrictTypes,
		NeedMatchBase:              app.NeedMatchBase,
		MarkStar:                   app.MarkStar,
		MarkStar1:                  app.MarkStar1,
		StarTypes1:                 app.StarTypes1,
		MarkStar2:                  app.MarkStar2,
		StarTypes2:                 app.StarTypes2,
		MsgLenMax:                  app.MsgLenMax,
		MsgDigitalCountMax:         app.MsgDigitalCountMax,
		BusinessModerationCheckUrl: app.BusinessModerationCheckUrl,
		TextCountExpire:            app.TextCountExpire,
		DupliTextCount:             app.DupliTextCount,
		IsBlackDupliUser:           app.IsBlackDupliUser,
		IsAddWordStore:             app.IsAddWordStore,
	}, nil
}

// GetGdAppOfAppSecret 根据聊天室 app id 获取其抄送配置及绑定的反垃圾应用密钥
func (s *Server) GetGdAppOfAppSecret(ctx context.Context, req *servicepb.GetGdAppSecretOfAppRequest) (
	reply *servicepb.GetGdAppSecretOfAppReply, err error) {
	tr := startRPC("GetGdAppOfAppSecret", req.GetMsgTraceId(), zapcore.DebugLevel, zap.String("app_id", req.GetAppId()))
	defer func() { tr.finish(reply, err) }()

	app := s.apps.App(ctx, req.GetAppId())
	if app == nil {
		return &servicepb.GetGdAppSecretOfAppReply{Ret: notExistsResponse()}, nil
	}
	reply = &servicepb.GetGdAppSecretOfAppReply{
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
	}
	gd := s.apps.GdApp(ctx, app.GdAppId)
	if gd == nil {
		reply.Ret = notExistsResponse()
		return reply, nil
	}
	reply.GdAppSecret = gd.AppSecret
	reply.Ret = successResponse()
	return reply, nil
}

// QueryGdAppInfo 查询反垃圾应用配置
func (s *Server) QueryGdAppInfo(ctx context.Context, req *servicepb.QueryGdAppInfoRequest) (
	reply *servicepb.QueryGdAppInfoReply, err error) {
	tr := startRPC("QueryGdAppInfo", req.GetMsgTraceId(), zapcore.DebugLevel, zap.Uint64("id", req.GetId()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.QueryGdAppInfoReply{Ret: internalErrorResponse()}
	app, err := s.store.GdAppByID(ctx, req.GetId())
	if err != nil {
		return reply, internalError(err)
	}
	if app == nil {
		reply.Ret = notExistsResponse()
		return reply, nil
	}
	reply.Ret = successResponse()
	reply.Info = gdAppToPB(app)
	return reply, nil
}
