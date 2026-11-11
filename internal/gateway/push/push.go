// Package push delivers chatroom events from Kafka and Redis to the clients
// connected to this gateway.
package push

import (
	"context"
	"fmt"
	"strconv"

	"github.com/IBM/sarama"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/clientpb"
	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/gateway/convert"
	"github.com/EdwinChanYi/orbikit/internal/gateway/session"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// Topics names the consumed Kafka topics.
type Topics struct {
	// Broadcast carries servicepb.ChatroomBcst for room members.
	Broadcast string `yaml:"broadcast"`
	// FullServer carries servicepb.ChatroomBcst for every user of an app.
	FullServer string `yaml:"fullserver"`
	// Kicked carries servicepb.ChatRoomMemberBeKickedBcst.
	Kicked string `yaml:"kicked"`
	// Event carries servicepb.ChatRoomMiscEventMsg.
	Event string `yaml:"event"`
}

// List returns the configured topics.
func (t Topics) List() []string {
	return []string{t.Broadcast, t.FullServer, t.Kicked, t.Event}
}

// Dispatcher routes Kafka messages to local sessions.
type Dispatcher struct {
	sessions *session.Manager
	topics   Topics
	receipt  bool
}

// NewDispatcher returns a Dispatcher. receipt asks clients to acknowledge
// room messages.
func NewDispatcher(sessions *session.Manager, topics Topics, receipt bool) *Dispatcher {
	return &Dispatcher{sessions: sessions, topics: topics, receipt: receipt}
}

// Handle implements kafkax.HandlerFunc.
func (d *Dispatcher) Handle(_ context.Context, msg *sarama.ConsumerMessage) error {
	switch msg.Topic {
	case d.topics.Broadcast:
		in := &servicepb.ChatroomBcst{}
		if err := proto.Unmarshal(msg.Value, in); err != nil {
			return fmt.Errorf("unmarshal room broadcast: %w", err)
		}
		return d.roomBroadcast(in)
	case d.topics.FullServer:
		in := &servicepb.ChatroomBcst{}
		if err := proto.Unmarshal(msg.Value, in); err != nil {
			return fmt.Errorf("unmarshal full-server broadcast: %w", err)
		}
		return d.fullServerBroadcast(in)
	case d.topics.Kicked:
		in := &servicepb.ChatRoomMemberBeKickedBcst{}
		if err := proto.Unmarshal(msg.Value, in); err != nil {
			return fmt.Errorf("unmarshal kicked broadcast: %w", err)
		}
		return d.kicked(in)
	case d.topics.Event:
		in := &servicepb.ChatRoomMiscEventMsg{}
		if err := proto.Unmarshal(msg.Value, in); err != nil {
			return fmt.Errorf("unmarshal misc event: %w", err)
		}
		return d.miscEvent(in)
	}
	return nil
}

// pushTo delivers raw to the local sessions of uids, at most once per uid.
func (d *Dispatcher) pushTo(appID string, uids, excluded []int64, cmd clientpb.CmdId, raw []byte) int {
	skip := make(map[int64]struct{}, len(excluded)+len(uids))
	for _, uid := range excluded {
		skip[uid] = struct{}{}
	}
	n := 0
	for _, uid := range uids {
		if _, ok := skip[uid]; ok {
			continue
		}
		skip[uid] = struct{}{}
		if s := d.sessions.Lookup(appID, uid); s != nil && s.Push(cmd, raw) {
			n++
		}
	}
	return n
}

// roomBroadcast relies on the chatroom service resolving the recipients
// into IncludedUid.
func (d *Dispatcher) roomBroadcast(in *servicepb.ChatroomBcst) error {
	raw, err := proto.Marshal(convert.BroadcastMessage(in, false, d.receipt))
	if err != nil {
		return err
	}
	n := d.pushTo(in.AppId, in.IncludedUid, in.ExcludedUid, clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, raw)
	log.L().Debug("room broadcast",
		zap.String("app_id", in.AppId),
		zap.Int64("room_id", in.ChatroomId),
		zap.String("msg_id", in.MsgId),
		zap.Int("recipients", len(in.IncludedUid)),
		zap.Int("delivered", n))
	return nil
}

func (d *Dispatcher) fullServerBroadcast(in *servicepb.ChatroomBcst) error {
	raw, err := proto.Marshal(convert.BroadcastMessage(in, true, d.receipt))
	if err != nil {
		return err
	}
	n := 0
	d.sessions.RangeApp(in.AppId, func(s *session.Session) bool {
		if s.Push(clientpb.CmdId_CHATROOM_RECEIVE_MESSAGE, raw) {
			n++
		}
		return true
	})
	log.L().Debug("full-server broadcast", zap.String("app_id", in.AppId), zap.String("msg_id", in.MsgId), zap.Int("delivered", n))
	return nil
}

func (d *Dispatcher) kicked(in *servicepb.ChatRoomMemberBeKickedBcst) error {
	raw, err := proto.Marshal(convert.KickedBcst(in))
	if err != nil {
		return err
	}
	n := d.pushTo(in.AppId, in.ToUid, nil, clientpb.CmdId_CHATROOM_HAS_BEEN_KICKED, raw)
	log.L().Debug("kicked broadcast",
		zap.String("app_id", in.AppId),
		zap.Int64("room_id", in.RoomId),
		zap.String("reason", in.KickedReason.String()),
		zap.Int("delivered", n))
	return nil
}

func (d *Dispatcher) miscEvent(in *servicepb.ChatRoomMiscEventMsg) error {
	switch in.Type {
	case servicepb.ChatRoomMiscEventType_MISC_EVENT_USER_RESTRAIN:
		info := in.GetRestrainInfo()
		if info == nil {
			return fmt.Errorf("restrain event without payload, trace %s", in.MsgTraceId)
		}
		if s := d.sessions.Lookup(in.AppId, info.Uid); s != nil {
			log.L().Info("disconnect restrained user", zap.String("app_id", in.AppId), zap.Int64("uid", info.Uid))
			s.Close()
		}
	case servicepb.ChatRoomMiscEventType_MISC_EVENT_UPLOAD_CLIENT_LOG:
		info := in.GetUploadLog()
		if info == nil {
			return fmt.Errorf("upload-log event without payload, trace %s", in.MsgTraceId)
		}
		s := d.sessions.Lookup(in.AppId, info.Uid)
		if s == nil {
			return nil
		}
		id, _ := strconv.ParseUint(in.MsgTraceId, 10, 64)
		raw, err := proto.Marshal(&clientpb.UploadLogBroadcast{
			Uid:         info.Uid,
			Time:        info.LogDate,
			Type:        clientpb.LogType(info.LogType),
			UploadToken: info.UploadToken,
			Id:          int64(id),
		})
		if err != nil {
			return err
		}
		s.Push(clientpb.CmdId_UPLOAD_LOG_BROADCAST, raw)
		log.L().Info("upload log requested", zap.String("app_id", in.AppId), zap.Int64("uid", info.Uid), zap.String("trace_id", in.MsgTraceId))
	default:
		return fmt.Errorf("unknown misc event type %d", in.Type)
	}
	return nil
}
