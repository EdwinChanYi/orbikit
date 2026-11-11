package server

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

// RestraintUserOperator 封禁（action=1）或解封（其他值）用户
func (s *Server) RestraintUserOperator(ctx context.Context, req *servicepb.RestraintUserOperatorRequest) (
	reply *servicepb.RestraintUserOperatorReply, err error) {
	tr := startRPC("RestraintUserOperator", req.GetMsgTraceId(), zapcore.InfoLevel,
		zap.String("app_id", req.GetAppId()),
		zap.Int64("uid", req.GetUid()),
		zap.Int32("action", req.GetAction()))
	defer func() { tr.finish(reply, err) }()

	reply = &servicepb.RestraintUserOperatorReply{Ret: internalErrorResponse()}
	if err = s.restrain.Operate(ctx, req.GetAppId(), req.GetUid(), req.GetAction()); err != nil {
		return reply, internalError(err)
	}
	reply.Ret = successResponse()
	return reply, nil
}
