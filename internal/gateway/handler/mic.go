package handler

import (
	"context"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/convert"
)

func (s *Service) micUp(ctx context.Context, r *Request, body []byte) *clientpb.UpChatroomMicroResp {
	req := &clientpb.UpChatroomMicroReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.UpChatroomMicroResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.UpMic(cctx, &servicepb.UpMicRequest{
		RoomId:     req.RoomId,
		MicIndex:   req.Position,
		Uid:        r.UID,
		AppId:      r.AppID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "UpMic", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.UpChatroomMicroResp{Ret: e}
	}
	return &clientpb.UpChatroomMicroResp{Ret: ok()}
}

func (s *Service) micDown(ctx context.Context, r *Request, body []byte) *clientpb.DownChatroomMicroResp {
	req := &clientpb.DownChatroomMicroReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.DownChatroomMicroResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.DownMic(cctx, &servicepb.DownMicRequest{
		RoomId:     req.RoomId,
		MicIndex:   req.Position,
		Uid:        r.UID,
		AppId:      r.AppID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "DownMic", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.DownChatroomMicroResp{Ret: e}
	}
	return &clientpb.DownChatroomMicroResp{Ret: ok()}
}

func (s *Service) micKick(ctx context.Context, r *Request, body []byte) *clientpb.KickChatroomMicroResp {
	req := &clientpb.KickChatroomMicroReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.KickChatroomMicroResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.KickMic(cctx, &servicepb.KickMicRequest{
		RoomId:     req.RoomId,
		MicIndex:   int32(req.Position),
		Uid:        r.UID,
		AppId:      r.AppID,
		KicUid:     req.UserId,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "KickMic", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.KickChatroomMicroResp{Ret: e}
	}
	return &clientpb.KickChatroomMicroResp{Ret: ok()}
}

func (s *Service) micInvite(ctx context.Context, r *Request, body []byte) *clientpb.InviteChatroomMicroResp {
	req := &clientpb.InviteChatroomMicroReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.InviteChatroomMicroResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.InviteMic(cctx, &servicepb.InviteMicRequest{
		RoomId:     req.RoomId,
		MicIndex:   int32(req.Position),
		Uid:        r.UID,
		AppId:      r.AppID,
		InviteUid:  req.UserId,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "InviteMic", err, rs.GetRet(), clientpb.ErrorCode_CHATROOM_SERVICE_ERROR); e != nil {
		return &clientpb.InviteChatroomMicroResp{Ret: e}
	}
	return &clientpb.InviteChatroomMicroResp{Ret: ok()}
}

// micEdit locks/unlocks a position or mutes/unmutes its microphone. The
// position status takes precedence when both are set.
func (s *Service) micEdit(ctx context.Context, r *Request, body []byte) *clientpb.EditMicroPositionInfoRes {
	req := &clientpb.EditMicroPositionInfoReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.EditMicroPositionInfoRes{Ret: e}
	}
	cctx, cancel := context.WithTimeout(ctx, 2*s.opts.RPCTimeout)
	defer cancel()

	var (
		method string
		mics   []*servicepb.MicInfo
		sret   *servicepb.UnifiedResponse
		err    error
	)
	switch {
	case req.PositionStatus != clientpb.MicroPositionStatus_UNDEFINED:
		method = "BlockMic"
		var rs *servicepb.BlockMicReply
		rs, err = s.Chatroom.BlockMic(cctx, &servicepb.BlockMicRequest{
			RoomId:     req.RoomId,
			MicIndex:   req.Position,
			Uid:        r.UID,
			AppId:      r.AppID,
			Action:     boolAction(req.PositionStatus == clientpb.MicroPositionStatus_MICRO_POSITION_STATE_LOCK),
			MsgTraceId: r.TraceID,
		})
		mics, sret = rs.GetMicInfo(), rs.GetRet()
	case req.PositionMicroStatus != clientpb.MicroPositionMicStatus_UNDEFINED_STATUS:
		method = "MuteMic"
		var rs *servicepb.MuteMicReply
		rs, err = s.Chatroom.MuteMic(cctx, &servicepb.MuteMicRequest{
			RoomId:     req.RoomId,
			MicIndex:   int32(req.Position),
			Uid:        r.UID,
			AppId:      r.AppID,
			Action:     boolAction(req.PositionMicroStatus == clientpb.MicroPositionMicStatus_MICRO_POSITION_AUDIO_STATE_MUTE),
			MsgTraceId: r.TraceID,
		})
		mics, sret = rs.GetMicInfo(), rs.GetRet()
	default:
		return &clientpb.EditMicroPositionInfoRes{Ret: ok()}
	}

	if err != nil || sret == nil {
		return &clientpb.EditMicroPositionInfoRes{Ret: rpcRet(r, method, err, nil, clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR)}
	}
	res := &clientpb.EditMicroPositionInfoRes{Ret: ok(), RoomMicros: convert.Micros(mics, true)}
	if e := rpcRet(r, method, nil, sret, clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR); e != nil {
		res.Ret = e
	}
	return res
}

func (s *Service) micInfo(ctx context.Context, r *Request, body []byte) *clientpb.QueryChatroomMicroResp {
	req := &clientpb.QueryChatroomMicroReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.QueryChatroomMicroResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.QueryMicInfo(cctx, &servicepb.QueryMicInfoRequest{
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		Uid:        r.UID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "QueryMicInfo", err, rs.GetRet(), clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR); e != nil {
		return &clientpb.QueryChatroomMicroResp{Ret: e}
	}
	return &clientpb.QueryChatroomMicroResp{Ret: ok(), RoomMicros: convert.Micros(rs.MicInfo, true)}
}

func (s *Service) micTokenUpdate(ctx context.Context, r *Request, body []byte) *clientpb.UpdateChatroomMicroTokenResp {
	req := &clientpb.UpdateChatroomMicroTokenReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.UpdateChatroomMicroTokenResp{Ret: e}
	}
	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.UpdateMicToken(cctx, &servicepb.UpdateMicTokenRequest{
		Token:      req.Token,
		RoomId:     req.RoomId,
		AppId:      r.AppID,
		Uid:        r.UID,
		MsgTraceId: r.TraceID,
	})
	if e := rpcRet(r, "UpdateMicToken", err, rs.GetRet(), clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR); e != nil {
		return &clientpb.UpdateChatroomMicroTokenResp{Ret: e}
	}
	return &clientpb.UpdateChatroomMicroTokenResp{Ret: ok()}
}
