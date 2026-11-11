// Package convert maps between internal service messages and client messages.
package convert

import (
	"encoding/binary"
	"sort"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// KickedBcst converts a chatroom kick notification for the client.
func KickedBcst(msg *servicepb.ChatRoomMemberBeKickedBcst) *clientpb.ChatRoomMemberBeKickedBcst {
	if msg == nil {
		return nil
	}
	return &clientpb.ChatRoomMemberBeKickedBcst{
		RoomId:       msg.RoomId,
		KickedReason: kickReason(msg.KickedReason),
		Duration:     msg.Duration,
		RemoteExt:    msg.RemoteExt,
		NotifyExt:    msg.NotifyExt,
	}
}

func kickReason(r servicepb.ChatroomKickReason) clientpb.ChatroomKickReason {
	switch r {
	case servicepb.ChatroomKickReason_KICK_BY_CHATROOM_INVALID:
		return clientpb.ChatroomKickReason_KICK_BY_CHATROOM_INVALID
	case servicepb.ChatroomKickReason_KICK_BY_PERSON:
		return clientpb.ChatroomKickReason_KICK_BY_PERSON
	case servicepb.ChatroomKickReason_KICK_BY_CONFLICT_LOGIN:
		return clientpb.ChatroomKickReason_KICK_BY_CONFLICT_LOGIN
	case servicepb.ChatroomKickReason_KICK_BY_BLACKLIST:
		return clientpb.ChatroomKickReason_KICK_BY_BLACKLIST
	default:
		return clientpb.ChatroomKickReason_KICK_BY_UNDEFINED
	}
}

// BroadcastMessage converts a chatroom broadcast into the client message.
// fullServer marks app-wide broadcasts, which use the system session type.
func BroadcastMessage(in *servicepb.ChatroomBcst, fullServer, receipt bool) *clientpb.Message {
	out := &clientpb.Message{
		MessageType: messageType(in.MsgType),
		From:        in.From,
		MessageId:   in.MsgId,
		Session: &clientpb.Session{
			SessionType: clientpb.SessionType_SESSION_TYPE_CHATROOM,
			SessionId:   in.ChatroomId,
		},
		SenderClientType: clientTypeToClient(in.ClientType),
		Timestamp:        in.CreateTime,
	}
	if fullServer {
		out.Session.SessionType = clientpb.SessionType_SESSION_TYPE_SYSTEM
	}
	if receipt {
		out.Setting = &clientpb.MessageSetting{ReceiptForReceive: true}
	}
	if in.FromMemberInfo != nil {
		out.CustomInfo = in.FromMemberInfo.CustomInfo
	}

	switch out.MessageType {
	case clientpb.MessageType_MESSAGE_TYPE_TEXT:
		out.Text = in.GetTextContent()
	case clientpb.MessageType_MESSAGE_TYPE_CUSTOM:
		out.Ext = &clientpb.Message_MessageObject{MessageObject: in.GetCustomContent()}
	case clientpb.MessageType_MESSAGE_TYPE_NOTIFICATION:
		if ev := in.GetEventContent(); ev != nil {
			out.Ext = &clientpb.Message_Notification{
				Notification: &clientpb.NotificationContent{
					Type: clientpb.NotificationType_CHATROOM,
					Content: &clientpb.NotificationContent_ChatroomNotificationContent{
						ChatroomNotificationContent: notification(ev),
					},
				},
			}
		}
	}
	return out
}

func eventType(t servicepb.ChatroomEventType) clientpb.ChatroomEventType {
	switch t {
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ENTER:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_ENTER
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_EXIT:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_EXIT
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDBLACK:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDBLACK
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEBLACK:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEBLACK
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDMUTE:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDMUTE
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEMUTE:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEMUTE
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_CLOSED:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_CLOSED
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_INFOUPDATED:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_INFOUPDATED
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_KICKED:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_KICKED
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_UNKICKED:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_UNKICKED
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MEMBERUPDATEINFO:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_MEMBERUPDATEINFO
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROCHANGE:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROCHANGE
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROSTATUSCHANGE:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROSTATUSCHANGE
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ROOMMUTED:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_ROOMMUTED
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ROOMUNMUTED:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_ROOMUNMUTED
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ROLECHANGE:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_ROLECHANGE
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_USERONLINE:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_USERONLINE
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_USEROFFLINE:
		return clientpb.ChatroomEventType_CHATROOM_EVENTTYPE_USEROFFLINE
	default:
		return clientpb.ChatroomEventType_UNDEFINED
	}
}

// beInt32 decodes the 4-byte big-endian integers the chatroom service puts in
// event content.
func beInt32(b []byte) (int32, bool) {
	if len(b) < 4 {
		return 0, false
	}
	return int32(binary.BigEndian.Uint32(b[:4])), true
}

func notification(ev *servicepb.ChatroomEvent) *clientpb.ChatroomNotificationContent {
	out := &clientpb.ChatroomNotificationContent{
		EventType: eventType(ev.Type),
		Source:    MemberBaseInfo(ev.FromMemberInfo, false),
		RemoteExt: ev.Attachment,
		NotifyExt: ev.NotifyData,
	}

	switch ev.Type {
	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROCHANGE,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROSTATUSCHANGE:
		micros := &clientpb.ChatroomMicroNotification{}
		for k, v := range ev.EventContent {
			switch k {
			case "data":
				reply := &servicepb.QueryMicInfoReply{}
				if err := proto.Unmarshal(v, reply); err != nil {
					log.L().Warn("decode mic event data", zap.Error(err))
					continue
				}
				micros.Micros = Micros(reply.MicInfo, false)
			case "op":
				if n, ok := beInt32(v); ok {
					micros.Operate = clientpb.MicroPositionOperate(n)
				}
			case "oldPos":
				if n, ok := beInt32(v); ok {
					micros.OldPosition = n
				}
			case "newPos":
				if n, ok := beInt32(v); ok {
					micros.NewPosition = n
				}
			}
		}
		out.Ext = &clientpb.ChatroomNotificationContent_Micros{Micros: micros}

	case servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MEMBERUPDATEINFO,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDBLACK,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEBLACK,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ADDMUTE,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_REMOVEMUTE,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_KICKED,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_UNKICKED,
		servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_ROLECHANGE:
		group := &clientpb.ChatroomNotificationGroup{}
		group.Members = append(group.Members, MemberBaseInfo(ev.ToMemberInfo, true))
		out.Ext = &clientpb.ChatroomNotificationContent_Targets{Targets: group}

	default:
		for _, v := range ev.EventContent {
			info := &servicepb.ChatroomInfo{}
			if err := proto.Unmarshal(v, info); err != nil {
				log.L().Warn("decode chatroom event data", zap.Error(err))
				continue
			}
			out.Ext = &clientpb.ChatroomNotificationContent_Chatroom{Chatroom: Chatroom(info)}
		}
	}
	return out
}

func muteStatus(m *servicepb.ChatroomMemberInfo) *clientpb.MuteStatus {
	var end int64
	if m.IsInMuteList && m.MuteFinish >= time.Now().UnixNano() {
		end = m.MuteFinish / int64(time.Millisecond)
	}
	return &clientpb.MuteStatus{IsMute: m.IsInMuteList, EndTime: end}
}

func kickStatus(m *servicepb.ChatroomMemberInfo) *clientpb.KickStatus {
	var end int64
	if m.IsInKickList && m.KickFinish >= time.Now().UnixNano() {
		end = m.KickFinish / int64(time.Millisecond)
	}
	return &clientpb.KickStatus{IsKick: m.IsInKickList, EndTime: end}
}

// MemberBaseInfo converts a member; withSearchKeys also copies search keys.
func MemberBaseInfo(m *servicepb.ChatroomMemberInfo, withSearchKeys bool) *clientpb.ChatroomMemberBaseInfo {
	if m == nil || m.Uid == 0 {
		return nil
	}
	out := &clientpb.ChatroomMemberBaseInfo{
		UserId:            m.Uid,
		RoomNickname:      m.Nickname,
		RoomAvatar:        m.Avatar,
		CustomInfo:        m.CustomInfo,
		ChatroomRemoteExt: m.ChatroomRemoteExt,
		Role:              m.RoleId,
		IsInBlockList:     m.IsInBlockList,
		MuteStatus:        muteStatus(m),
		KickStatus:        kickStatus(m),
		IsOnline:          m.IsOnline,
		EnterTimeInterval: m.EnterTime,
		IsRobot:           m.IsReboot,
	}
	if withSearchKeys {
		out.SearchKeys = m.SearchKeys
	}
	return out
}

func messageType(t servicepb.ChatroomMsgType) clientpb.MessageType {
	switch t {
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_UNDEFINED:
		return clientpb.MessageType_MESSAGE_TYPE_UNDEFINED
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT:
		return clientpb.MessageType_MESSAGE_TYPE_TEXT
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM:
		return clientpb.MessageType_MESSAGE_TYPE_CUSTOM
	case servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_EVENT:
		return clientpb.MessageType_MESSAGE_TYPE_NOTIFICATION
	default:
		log.L().Warn("unknown chatroom message type", zap.Int32("msg_type", int32(t)))
		return clientpb.MessageType_MESSAGE_TYPE_UNDEFINED
	}
}

// MessageTypeToService maps a client message type for SendChatroomMsg.
func MessageTypeToService(t clientpb.MessageType) servicepb.ChatroomMsgType {
	switch t {
	case clientpb.MessageType_MESSAGE_TYPE_TEXT:
		return servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT
	case clientpb.MessageType_MESSAGE_TYPE_CUSTOM:
		return servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM
	default:
		return servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_UNDEFINED
	}
}

func clientTypeToClient(t servicepb.ClientType) clientpb.ClientType {
	switch t {
	case servicepb.ClientType_CLIENT_TYPE_ANDROID:
		return clientpb.ClientType_CLIENT_TYPE_ANDROID
	case servicepb.ClientType_CLIENT_TYPE_IOS:
		return clientpb.ClientType_CLIENT_TYPE_IOS
	case servicepb.ClientType_CLIENT_TYPE_PC:
		return clientpb.ClientType_CLIENT_TYPE_PC
	case servicepb.ClientType_CLIENT_TYPE_MACOS:
		return clientpb.ClientType_CLIENT_TYPE_MACOS
	case servicepb.ClientType_CLIENT_TYPE_WEB:
		return clientpb.ClientType_CLIENT_TYPE_WEB
	case servicepb.ClientType_CLIENT_TYPE_RESTFUL:
		return clientpb.ClientType_CLIENT_TYPE_RESTFUL
	case servicepb.ClientType_CLIENT_TYPE_SYSTEM:
		return clientpb.ClientType_CLIENT_TYPE_SYSTEM
	default:
		return clientpb.ClientType_CLIENT_TYPE_UNDEFINED
	}
}

// ClientTypeToService maps the sender's client type.
func ClientTypeToService(t clientpb.ClientType) servicepb.ClientType {
	switch t {
	case clientpb.ClientType_CLIENT_TYPE_ANDROID:
		return servicepb.ClientType_CLIENT_TYPE_ANDROID
	case clientpb.ClientType_CLIENT_TYPE_IOS:
		return servicepb.ClientType_CLIENT_TYPE_IOS
	case clientpb.ClientType_CLIENT_TYPE_PC:
		return servicepb.ClientType_CLIENT_TYPE_PC
	case clientpb.ClientType_CLIENT_TYPE_MACOS:
		return servicepb.ClientType_CLIENT_TYPE_MACOS
	case clientpb.ClientType_CLIENT_TYPE_WEB:
		return servicepb.ClientType_CLIENT_TYPE_WEB
	case clientpb.ClientType_CLIENT_TYPE_RESTFUL:
		return servicepb.ClientType_CLIENT_TYPE_RESTFUL
	case clientpb.ClientType_CLIENT_TYPE_SYSTEM:
		return servicepb.ClientType_CLIENT_TYPE_SYSTEM
	default:
		return servicepb.ClientType_CLIENT_TYPE_UNDEFINED
	}
}

// Chatroom converts room info.
func Chatroom(r *servicepb.ChatroomInfo) *clientpb.Chatroom {
	if r == nil {
		return nil
	}
	return &clientpb.Chatroom{
		RoomId:       r.ChatroomId,
		RoomName:     r.Name,
		Announcement: r.Announcement,
		Owner:        r.OwnerUid,
		OnlineCount:  r.OnlineCount,
		IsMuteAll:    r.IsMuteAll,
		RemoteExt:    r.Ext,
	}
}

func positionStatus(locked bool) clientpb.MicroPositionStatus {
	if locked {
		return clientpb.MicroPositionStatus_MICRO_POSITION_STATE_LOCK
	}
	return clientpb.MicroPositionStatus_MICRO_POSITION_STATE_FREE
}

func micStatus(muted bool) clientpb.MicroPositionMicStatus {
	if muted {
		return clientpb.MicroPositionMicStatus_MICRO_POSITION_AUDIO_STATE_MUTE
	}
	return clientpb.MicroPositionMicStatus_MICRO_POSITION_AUDIO_STATE_OPEN
}

// Micros converts mic positions. sorted orders them by position and copies
// member search keys, as mic queries do.
func Micros(in []*servicepb.MicInfo, sorted bool) []*clientpb.ChatroomMicro {
	if in == nil {
		return nil
	}
	out := make([]*clientpb.ChatroomMicro, 0, len(in))
	for _, m := range in {
		out = append(out, &clientpb.ChatroomMicro{
			Position:       m.Position,
			ChatroomMember: MemberBaseInfo(m.MemberInfo, sorted),
			Ext:            m.Ext,
			PositionStatus: positionStatus(m.IsLocked),
			MicroStatus:    micStatus(m.IsMuted),
		})
	}
	if sorted {
		sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	}
	return out
}

// ErrorCode maps a service error code to the client error code.
func ErrorCode(code servicepb.ErrorCode) clientpb.ErrorCode {
	switch code {
	case servicepb.ErrorCode_SUCCESS:
		return clientpb.ErrorCode_SUCCESS
	case servicepb.ErrorCode_CHATROOM_SERVICE_ERROR:
		return clientpb.ErrorCode_CHATROOM_SERVICE_ERROR
	case servicepb.ErrorCode_CHATROOM_NOT_EXISTS:
		return clientpb.ErrorCode_CHATROOM_NOT_EXISTS
	case servicepb.ErrorCode_CHATROOM_USER_NOT_IN_TARGET_ROOM:
		return clientpb.ErrorCode_CHATROOM_USER_NOT_IN_TARGET_ROOM
	case servicepb.ErrorCode_CHATROOM_UP_MIC_ERROR_MIC_HAS_BEEN_LOCKED:
		return clientpb.ErrorCode_CHATROOM_UP_MIC_ERROR_MIC_HAS_BEEN_LOCKED
	case servicepb.ErrorCode_CHATROOM_UP_MIC_ERROR_MIC_HAS_USER:
		return clientpb.ErrorCode_CHATROOM_UP_MIC_ERROR_MIC_HAS_USER
	case servicepb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS:
		return clientpb.ErrorCode_CHATROOM_MIC_INDEX_NOT_EXISTS
	case servicepb.ErrorCode_CHATROOM_DOWN_MIC_ERROR_MIC_HAS_NOT_USER:
		return clientpb.ErrorCode_CHATROOM_DOWN_MIC_ERROR_MIC_HAS_NOT_USER
	case servicepb.ErrorCode_CHATROOM_DOWN_MIC_ERROR_USER_NOT_IN_TARGET_INDEX:
		return clientpb.ErrorCode_CHATROOM_DOWN_MIC_ERROR_USER_NOT_IN_TARGET_INDEX
	case servicepb.ErrorCode_CHATROOM_CLOSED:
		return clientpb.ErrorCode_CHATROOM_CLOSED
	case servicepb.ErrorCode_CHATROOM_ALREADY_IN_ROOM:
		return clientpb.ErrorCode_CHATROOM_ALREADY_IN_ROOM
	case servicepb.ErrorCode_CHATROOM_USER_PERMISSION_ERROR,
		servicepb.ErrorCode_CHATROOM_MEMBER_NOT_PERMISSION:
		return clientpb.ErrorCode_CHATROOM_USER_PERMISSION_ERROR
	case servicepb.ErrorCode_CHATROOM_NOT_IN_ROOM:
		return clientpb.ErrorCode_CHATROOM_NOT_IN_ROOM
	case servicepb.ErrorCode_CHATROOM_MIC_HAS_NOT_USER:
		return clientpb.ErrorCode_CHATROOM_MIC_HAS_NOT_USER
	case servicepb.ErrorCode_CHATROOM_ENTER_TOKEN_ERROR:
		return clientpb.ErrorCode_CHATROOM_ENTER_TOKEN_ERR
	case servicepb.ErrorCode_CHATROOM_MEMBER_MESSAGE_NOT_EXISTS:
		return clientpb.ErrorCode_CHATROOM_MEMBER_MESSAGE_NOT_EXISTS
	case servicepb.ErrorCode_CHATROOM_MEMBER_NOT_MODIFY_PERMISSION:
		return clientpb.ErrorCode_CCHATROOM_MEMBER_NOT_MODIFY_PERMISSION
	case servicepb.ErrorCode_CHATROOM_MEMBER_NOT_KICK_PERMISSION:
		return clientpb.ErrorCode_CHATROOM_MEMBER_NOT_KICK_PERMISSION
	case servicepb.ErrorCode_CHATROOM_USER_IS_MUTED:
		return clientpb.ErrorCode_CHATROOM_USER_IN_MUTE_LIST
	case servicepb.ErrorCode_CHATROOM_CHATROOM_IS_MUTED:
		return clientpb.ErrorCode_CHATROOM_IN_MUTE_STATUS
	case servicepb.ErrorCode_CHATROOM_MEMBER_BLOCK:
		return clientpb.ErrorCode_CHATROOM_USER_IN_BLOCK_LIST
	case servicepb.ErrorCode_CHATROOM_MEMBER_KIKY:
		return clientpb.ErrorCode_CHATROOM_USER_IN_KICKED_LIST
	case servicepb.ErrorCode_CHATROOM_ROOM_USER_LOCKED:
		return clientpb.ErrorCode_CHATROOM_ROOM_USER_LOCKED
	case servicepb.ErrorCode_CORE_SERVICE_ERROR:
		return clientpb.ErrorCode_CORESERVER_INTER_ERROR
	case servicepb.ErrorCode_CORE_LOGIN_TOKEN_NOT_EXISTS:
		return clientpb.ErrorCode_LOGIN_TOKEN_NOT_EXISTS
	default:
		log.L().Warn("unmapped service error code", zap.Int32("code", int32(code)))
		return clientpb.ErrorCode_CHATROOM_ERROR_CANNOT_CONVERT
	}
}
