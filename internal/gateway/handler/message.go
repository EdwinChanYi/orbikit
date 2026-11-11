package handler

import (
	"context"

	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/clientpb"
	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/gateway/convert"
	"github.com/yourname/roomkit/internal/gateway/moderation"
)

func (s *Service) sendMessage(ctx context.Context, r *Request, body []byte) *clientpb.SendMessageResp {
	req := &clientpb.SendMessageReq{}
	if e := unmarshal(r, body, req); e != nil {
		return &clientpb.SendMessageResp{Ret: e}
	}
	msg := req.Message
	if msg == nil || msg.Session == nil || msg.Session.SessionType != clientpb.SessionType_SESSION_TYPE_CHATROOM {
		return &clientpb.SendMessageResp{Ret: ret(clientpb.ErrorCode_MARS_AGENT_GET_BAD_PARAM)}
	}
	roomID := msg.Session.SessionId
	if msg.MessageId == "" {
		msg.MessageId = r.TraceID
	}

	moderated := msg.Text != "" && !isNone(s.Moderator)
	s.Store.IncrDailyMsg(ctx, r.AppID, moderated)
	if moderated {
		res, err := s.Moderator.CheckText(ctx, r.AppID, r.UID, roomID, msg.Text)
		if err != nil {
			r.Log.Error("moderation failed", zap.Error(err), zap.Int64("room_id", roomID))
			return &clientpb.SendMessageResp{Ret: ret(clientpb.ErrorCode_MARS_AGENT_GD_HTTP_ERR)}
		}
		if !res.Allowed {
			r.Log.Info("message rejected by moderation", zap.Int64("room_id", roomID), zap.String("reason", res.Reason))
			reason := res.Reason
			if reason == "" {
				reason = clientpb.ErrorCode_MARS_AGENT_GD_ILLEGAL.String()
			}
			return &clientpb.SendMessageResp{Ret: &clientpb.UnifiedResponse{
				ErrorCode: clientpb.ErrorCode_MARS_AGENT_GD_ILLEGAL,
				ErrorMsg:  reason,
			}}
		}
		msg.Text = res.Text
	}

	in := &servicepb.ChatroomMsgRequest{
		AppId:      r.AppID,
		From:       r.UID,
		ChatroomId: roomID,
		MsgType:    convert.MessageTypeToService(msg.MessageType),
		MsgId:      msg.MessageId,
		Ext:        msg.RemoteExt,
		ClientType: convert.ClientTypeToService(msg.SenderClientType),
		MsgTraceId: r.TraceID,
	}
	switch in.MsgType {
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT:
		in.Content = &servicepb.ChatroomMsgRequest_TextContent{TextContent: msg.Text}
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM:
		in.Content = &servicepb.ChatroomMsgRequest_CustomContent{CustomContent: msg.GetMessageObject()}
	}

	cctx, cancel := s.rpcCtx(ctx)
	defer cancel()
	rs, err := s.Chatroom.SendChatroomMsg(cctx, in)
	if e := rpcRet(r, "SendChatroomMsg", err, rs.GetRet(), clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR); e != nil {
		return &clientpb.SendMessageResp{Ret: e}
	}
	return &clientpb.SendMessageResp{Ret: ok()}
}

func isNone(m moderation.Moderator) bool {
	_, none := m.(moderation.None)
	return m == nil || none
}

// messageHistoryUnsupported answers both history commands: this gateway has
// no message history backend.
func (s *Service) messageHistoryUnsupported(_ context.Context, _ *Request, _ []byte) *clientpb.MessageQueryV2Resp {
	return &clientpb.MessageQueryV2Resp{Ret: &clientpb.UnifiedResponse{
		ErrorCode: clientpb.ErrorCode_MARS_AGENT_INTERNAL_ERROR,
		ErrorMsg:  "message history query is not supported",
	}}
}
