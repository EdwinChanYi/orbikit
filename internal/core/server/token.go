package server

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

// ApplyClientLoginToken 申请登录令牌
func (s *Server) ApplyClientLoginToken(ctx context.Context, req *servicepb.ApplyClientLoginTokenRequest) (
	reply *servicepb.ApplyClientLoginTokenReply, err error) {
	tr := startRPC("ApplyClientLoginToken", req.GetMsgTraceId(), zapcore.InfoLevel,
		zap.String("app_id", req.GetAppId()), zap.Int64("uid", req.GetUid()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.ApplyClientLoginTokenReply{Ret: internalErrorResponse()}
	token, restrained, err := s.tokens.Apply(ctx, req.GetAppId(), req.GetUid())
	switch {
	case err != nil:
		return reply, internalError(err)
	case token == "":
		tr.l.Warn("apply login token for invalid app")
		reply.Ret = notExistsResponse()
	case restrained:
		tr.l.Warn("apply login token for restrained user")
		reply.Ret = codeResponse(servicepb.ErrorCode_CORE_USER_IS_RESTRAIN)
	default:
		reply.Ret = successResponse()
		reply.LoginToken = token
	}
	return reply, nil
}

// GetClientLoginTokenInfo 根据登录令牌获取 appId 与 uid
func (s *Server) GetClientLoginTokenInfo(ctx context.Context, req *servicepb.GetClientLoginTokenInfoRequest) (
	reply *servicepb.GetClientLoginTokenInfoReply, err error) {
	tr := startRPC("GetClientLoginTokenInfo", req.GetMsgTraceId(), zapcore.DebugLevel)
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.GetClientLoginTokenInfoReply{Ret: internalErrorResponse()}
	appID, uid, err := s.tokens.Info(ctx, req.GetLoginToken())
	if err != nil {
		return reply, internalError(err)
	}
	if appID == "" || uid == 0 {
		reply.Ret = codeResponse(servicepb.ErrorCode_CORE_LOGIN_TOKEN_NOT_EXISTS)
		return reply, nil
	}
	reply.Ret = successResponse()
	reply.AppId = appID
	reply.Uid = uid
	return reply, nil
}

// DeleteClientLoginToken 删除登录令牌
func (s *Server) DeleteClientLoginToken(ctx context.Context, req *servicepb.DeleteClientLoginTokenReq) (
	reply *servicepb.DeleteClientLoginTokenResp, err error) {
	tr := startRPC("DeleteClientLoginToken", req.GetMsgTraceId(), zapcore.InfoLevel,
		zap.String("app_id", req.GetAppId()), zap.Int64("uid", req.GetUid()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.DeleteClientLoginTokenResp{Ret: internalErrorResponse()}
	code, err := s.tokens.Delete(ctx, req.GetAppId(), req.GetUid(), req.GetLoginToken())
	if err != nil {
		return reply, internalError(err)
	}
	if code == servicepb.ErrorCode_SUCCESS {
		reply.Ret = successResponse()
	} else {
		reply.Ret = codeResponse(code)
	}
	return reply, nil
}
