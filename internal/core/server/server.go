// Package server implements the Core gRPC service.
package server

import (
	"context"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/core/logic"
	"github.com/yourname/roomkit/internal/core/store"
	"github.com/yourname/roomkit/pkg/log"
)

// Notifier propagates app changes to other instances.
type Notifier interface {
	AppInfoUpdated(ctx context.Context, req *servicepb.AppInfoUpdatedRequest)
	AppCacheInvalidated(ctx context.Context, appID, traceID string)
}

// Server implements servicepb.CoreServer.
type Server struct {
	servicepb.UnimplementedCoreServer

	store    *store.Store
	apps     *logic.AppService
	tokens   *logic.TokenService
	restrain *logic.RestrainService
	notifier Notifier
}

var _ servicepb.CoreServer = (*Server)(nil)

// New returns a Core gRPC server.
func New(st *store.Store, apps *logic.AppService, tokens *logic.TokenService,
	restrain *logic.RestrainService, notifier Notifier) *Server {
	return &Server{store: st, apps: apps, tokens: tokens, restrain: restrain, notifier: notifier}
}

func newResponse(code servicepb.ErrorCode, msg string) *servicepb.UnifiedResponse {
	return &servicepb.UnifiedResponse{ErrorCode: code, ErrorMsg: msg}
}

func codeResponse(code servicepb.ErrorCode) *servicepb.UnifiedResponse {
	return newResponse(code, code.String())
}

func successResponse() *servicepb.UnifiedResponse {
	return newResponse(servicepb.ErrorCode_SUCCESS, "success")
}

func internalErrorResponse() *servicepb.UnifiedResponse {
	return newResponse(servicepb.ErrorCode_SERVER_INTERNAL_ERROR, "server internal error")
}

func notExistsResponse() *servicepb.UnifiedResponse {
	return codeResponse(servicepb.ErrorCode_CORE_APP_ID_NOT_EXISTS)
}

// internalError converts a storage failure into a gRPC error.
func internalError(err error) error {
	return status.Error(codes.Internal, err.Error())
}

// rpcTrace logs the start and end of one RPC.
type rpcTrace struct {
	l     *zap.Logger
	start time.Time
}

func startRPC(name, traceID string, lvl zapcore.Level, fields ...zap.Field) *rpcTrace {
	l := log.L().With(zap.String("rpc", name), zap.String("msg_trace_id", traceID))
	l.Log(lvl, "rpc begin", fields...)
	return &rpcTrace{l: l, start: time.Now()}
}

type withRet interface {
	GetRet() *servicepb.UnifiedResponse
}

func (t *rpcTrace) finish(reply withRet, err error) {
	if err != nil {
		t.l.Error("rpc failed", zap.Duration("used", time.Since(t.start)), zap.Error(err))
		return
	}
	t.l.Debug("rpc end",
		zap.Duration("used", time.Since(t.start)),
		zap.Stringer("error_code", reply.GetRet().GetErrorCode()))
}
