package delivery

import (
	"encoding/binary"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/pkg/log"
)

// Keys of ChatroomEvent.EventContent for microphone events. "data" holds a
// serialized QueryMicInfoReply; the others hold big-endian int32 values.
const (
	micKeyData   = "data"
	micKeyOp     = "op"
	micKeyOldPos = "oldPos"
	micKeyNewPos = "newPos"
)

// ToPostData converts a broadcast into the JSON payload posted to the app. It
// returns nil when the app has not enabled the message type or the message
// carries no copyable content.
func ToPostData(in *servicepb.ChatroomBcst, cfg *AppConfig) *servicepb.MsgCopyPostData {
	if !cfg.Enabled(in.GetMsgType()) {
		return nil
	}
	out := &servicepb.MsgCopyPostData{
		AppId:          in.GetAppId(),
		From:           in.GetFrom(),
		To:             in.GetTo(),
		FromMemberInfo: in.GetFromMemberInfo(),
		ToMemberInfo:   in.GetToMemberInfo(),
		ChatroomId:     in.GetChatroomId(),
		Ext:            in.GetExt(),
		ClientType:     in.GetClientType(),
		MsgType:        in.GetMsgType(),
		MsgId:          in.GetMsgId(),
		CreateTime:     in.GetCreateTime(),
	}
	switch in.GetMsgType() {
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT:
		out.TextContent = in.GetTextContent()
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM:
		out.CustomContent = in.GetCustomContent()
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_EVENT:
		ev := in.GetEventContent()
		if ev == nil {
			return nil
		}
		out.EventContent = toCopyEvent(ev)
	default:
		return nil
	}
	return out
}

func toCopyEvent(ev *servicepb.ChatroomEvent) *servicepb.MsgCopyChatroomEvent {
	out := &servicepb.MsgCopyChatroomEvent{
		Type:           ev.GetType(),
		From:           ev.GetFrom(),
		To:             ev.GetTo(),
		FromMemberInfo: ev.GetFromMemberInfo(),
		ToMemberInfo:   ev.GetToMemberInfo(),
		NotifyData:     ev.GetNotifyData(),
		Attachment:     ev.GetAttachment(),
	}

	switch ev.GetType() {
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROCHANGE,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROSTATUSCHANGE:
		out.MicOp = &servicepb.MicroOperation{}
		for k, v := range ev.GetEventContent() {
			if err := decodeMicField(out, k, v); err != nil {
				log.L().Warn("decode mic event content", zap.String("key", k), zap.Binary("value", v), zap.Error(err))
			}
		}

	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDBLACK,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEBLACK,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDMUTE,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEMUTE,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_KICKED,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_UNKICKED,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MEMBERUPDATEINFO,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ROLECHANGE:
		// 成员类事件只抄送成员信息

	default:
		// 其他房间事件的 EventContent 值为序列化的 ChatroomInfo
		for _, v := range ev.GetEventContent() {
			info := &servicepb.ChatroomInfo{}
			if err := proto.Unmarshal(v, info); err != nil {
				continue
			}
			out.RoomInfo = info
		}
	}
	return out
}

func decodeMicField(out *servicepb.MsgCopyChatroomEvent, key string, v []byte) error {
	switch key {
	case micKeyData:
		reply := &servicepb.QueryMicInfoReply{}
		if err := proto.Unmarshal(v, reply); err != nil {
			return err
		}
		out.MicInfos = copyMicInfos(reply.GetMicInfo())
	case micKeyOp, micKeyOldPos, micKeyNewPos:
		n, err := beInt32(v)
		if err != nil {
			return err
		}
		switch key {
		case micKeyOp:
			out.MicOp.Operate = n
		case micKeyOldPos:
			out.MicOp.OldPosition = n
		default:
			out.MicOp.NewPosition = n
		}
	}
	return nil
}

func beInt32(b []byte) (int32, error) {
	if len(b) < 4 {
		return 0, fmt.Errorf("int32 needs 4 bytes, got %d", len(b))
	}
	return int32(binary.BigEndian.Uint32(b)), nil
}

func copyMicInfos(in []*servicepb.MicInfo) []*servicepb.MicInfo {
	out := make([]*servicepb.MicInfo, 0, len(in))
	for _, m := range in {
		out = append(out, &servicepb.MicInfo{
			Position:   m.GetPosition(),
			IsLocked:   m.GetIsLocked(),
			IsMuted:    m.GetIsMuted(),
			MemberInfo: m.GetMemberInfo(),
			Ext:        m.GetExt(),
		})
	}
	return out
}
