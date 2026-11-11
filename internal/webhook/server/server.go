// Package server implements the MsgCopy gRPC service of the webhook service.
package server

import (
	"context"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/webhook/delivery"
	"github.com/yourname/roomkit/pkg/log"
)

// Server implements servicepb.MsgCopyServer.
type Server struct {
	servicepb.UnimplementedMsgCopyServer

	apps *delivery.AppCache
}

var _ servicepb.MsgCopyServer = (*Server)(nil)

// New returns a MsgCopy server that refreshes apps on configuration pushes.
func New(apps *delivery.AppCache) *Server {
	return &Server{apps: apps}
}

// AppInfoUpdated 应用配置变更，刷新本地抄送配置缓存
func (s *Server) AppInfoUpdated(_ context.Context, req *servicepb.AppInfoUpdatedRequest) (*servicepb.AppInfoUpdatedReply, error) {
	log.L().Info("rpc AppInfoUpdated",
		zap.String("msg_trace_id", req.GetMsgTraceId()),
		zap.String("app_id", req.GetAppId()),
		zap.String("msg_cp_url", req.GetMsgCpUrl()),
		zap.String("text_cp_url", req.GetTextCpUrl()),
		zap.String("custom_cp_url", req.GetCustomCpUrl()),
		zap.String("types", req.GetTypes()),
		zap.Int64("qps", req.GetQps()),
		zap.Int64("text_qps", req.GetTextQps()),
		zap.Int64("custom_qps", req.GetCustomQps()))

	s.apps.Put(req.GetAppId(), &delivery.AppConfig{
		Secret:      req.GetAppSecret(),
		MsgCpURL:    req.GetMsgCpUrl(),
		TextCpURL:   req.GetTextCpUrl(),
		CustomCpURL: req.GetCustomCpUrl(),
		Types:       delivery.ParseTypes(req.GetTypes()),
		QPS:         req.GetQps(),
		TextQPS:     req.GetTextQps(),
		CustomQPS:   req.GetCustomQps(),
	})
	return &servicepb.AppInfoUpdatedReply{
		Ret: &servicepb.UnifiedResponse{ErrorCode: servicepb.ErrorCode_SUCCESS, ErrorMsg: "success"},
	}, nil
}
