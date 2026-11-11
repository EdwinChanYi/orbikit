package delivery

import (
	"encoding/binary"
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

var allTypes = &AppConfig{Types: []int32{255}}

func be32(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

func TestParseTypesAndEnabled(t *testing.T) {
	if got := ParseTypes("1, 3,x,,255"); len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 255 {
		t.Fatalf("ParseTypes = %v", got)
	}
	c := &AppConfig{Types: ParseTypes("1,3")}
	if !c.Enabled(text) || !c.Enabled(event) || c.Enabled(custom) {
		t.Fatal("Enabled should follow the explicit list")
	}
	if !allTypes.Enabled(custom) {
		t.Fatal("255 enables every type")
	}
	if (&AppConfig{}).Enabled(text) {
		t.Fatal("no types means nothing is copied")
	}
}

func TestRoute(t *testing.T) {
	c := &AppConfig{MsgCpURL: "m", TextCpURL: "t", CustomCpURL: "c", QPS: 1, TextQPS: 2, CustomQPS: 3}
	for typ, want := range map[servicepb.ChatroomMsgType]struct {
		url string
		qps int64
	}{text: {"t", 2}, custom: {"c", 3}, event: {"m", 1}, servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_UNDEFINED: {"m", 0}} {
		if url, qps := c.Route(typ); url != want.url || qps != want.qps {
			t.Errorf("Route(%v) = %s,%d want %s,%d", typ, url, qps, want.url, want.qps)
		}
	}
}

func TestToPostDataText(t *testing.T) {
	in := &servicepb.ChatroomBcst{
		AppId: "app1", From: 1, To: 2, ChatroomId: 9, MsgType: text, MsgId: "m1", CreateTime: 123,
		Ext:         map[string]string{"k": "v"},
		IncludedUid: []int64{5}, ExcludedUid: []int64{6}, MsgTraceId: "trace",
		Content: &servicepb.ChatroomBcst_TextContent{TextContent: "hi"},
	}
	out := ToPostData(in, allTypes)
	if out == nil || out.TextContent != "hi" || out.AppId != "app1" || out.ChatroomId != 9 || out.CreateTime != 123 {
		t.Fatalf("unexpected %v", out)
	}
	if out.IncludedUid != nil || out.ExcludedUid != nil || out.MsgTraceId != "" {
		t.Fatal("routing fields must not be copied to the receiver")
	}
	if ToPostData(in, &AppConfig{Types: []int32{2}}) != nil {
		t.Fatal("text disabled -> nil")
	}
}

func TestToPostDataSkipsUnknownAndEmptyEvent(t *testing.T) {
	if ToPostData(&servicepb.ChatroomBcst{MsgType: servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_UNDEFINED}, allTypes) != nil {
		t.Fatal("undefined type -> nil")
	}
	if ToPostData(&servicepb.ChatroomBcst{MsgType: event}, allTypes) != nil {
		t.Fatal("event without content -> nil")
	}
}

func TestToPostDataMicEvent(t *testing.T) {
	data, err := proto.Marshal(&servicepb.QueryMicInfoReply{MicInfo: []*servicepb.MicInfo{
		{Position: 1, IsLocked: true, MemberInfo: &servicepb.ChatroomMemberInfo{Uid: 7}},
		{Position: 2, IsMuted: true, Ext: map[string]string{"a": "b"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	in := &servicepb.ChatroomBcst{AppId: "app1", MsgType: event, Content: &servicepb.ChatroomBcst_EventContent{
		EventContent: &servicepb.ChatroomEvent{
			Type: servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROCHANGE,
			From: 7,
			EventContent: map[string][]byte{
				"data": data, "op": be32(2), "oldPos": be32(-1), "newPos": be32(1), "bad": {1},
			},
			NotifyData: map[string]string{"n": "1"},
		},
	}}
	ev := ToPostData(in, allTypes).GetEventContent()
	if ev.GetMicOp().GetOperate() != 2 || ev.GetMicOp().GetOldPosition() != -1 || ev.GetMicOp().GetNewPosition() != 1 {
		t.Fatalf("mic op = %v", ev.GetMicOp())
	}
	if len(ev.GetMicInfos()) != 2 || ev.GetMicInfos()[0].GetMemberInfo().GetUid() != 7 || !ev.GetMicInfos()[1].GetIsMuted() {
		t.Fatalf("mic infos = %v", ev.GetMicInfos())
	}
	if ev.GetFrom() != 7 || ev.GetNotifyData()["n"] != "1" || ev.GetRoomInfo() != nil {
		t.Fatalf("event = %v", ev)
	}
}

func TestToPostDataShortMicFieldIgnored(t *testing.T) {
	in := &servicepb.ChatroomBcst{MsgType: event, Content: &servicepb.ChatroomBcst_EventContent{
		EventContent: &servicepb.ChatroomEvent{
			Type:         servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_MICROSTATUSCHANGE,
			EventContent: map[string][]byte{"op": {1, 2}},
		},
	}}
	ev := ToPostData(in, allTypes).GetEventContent()
	if ev.GetMicOp() == nil || ev.GetMicOp().GetOperate() != 0 {
		t.Fatalf("mic op = %v", ev.GetMicOp())
	}
}

func TestToPostDataRoomEvent(t *testing.T) {
	info, _ := proto.Marshal(&servicepb.ChatroomInfo{ChatroomId: 9, Name: "room", OnlineCount: 3})
	in := &servicepb.ChatroomBcst{MsgType: event, Content: &servicepb.ChatroomBcst_EventContent{
		EventContent: &servicepb.ChatroomEvent{
			Type:         servicepb.ChatroomEventType(1),
			EventContent: map[string][]byte{"info": info},
		},
	}}
	ev := ToPostData(in, allTypes).GetEventContent()
	if ev.GetRoomInfo().GetName() != "room" || ev.GetRoomInfo().GetOnlineCount() != 3 || ev.GetMicOp() != nil {
		t.Fatalf("event = %v", ev)
	}
}

func TestToPostDataMemberEventHasNoExtras(t *testing.T) {
	info, _ := proto.Marshal(&servicepb.ChatroomInfo{Name: "room"})
	in := &servicepb.ChatroomBcst{MsgType: event, Content: &servicepb.ChatroomBcst_EventContent{
		EventContent: &servicepb.ChatroomEvent{
			Type:         servicepb.ChatroomEventType_CHATROOM_EVENTTYPE_KICKED,
			To:           5,
			EventContent: map[string][]byte{"info": info},
		},
	}}
	ev := ToPostData(in, allTypes).GetEventContent()
	if ev.GetTo() != 5 || ev.GetRoomInfo() != nil || ev.GetMicOp() != nil {
		t.Fatalf("event = %v", ev)
	}
}

// TestPostBodyJSONShape pins the JSON field names and encodings receivers rely on.
func TestPostBodyJSONShape(t *testing.T) {
	in := &servicepb.ChatroomBcst{
		AppId: "app1", From: 1, ChatroomId: 9, MsgType: custom, MsgId: "m1", CreateTime: 123,
		ClientType: servicepb.ClientType(2),
		FromMemberInfo: &servicepb.ChatroomMemberInfo{
			Uid: 1, Nickname: "n", RoleId: []int32{1},
		},
		Content: &servicepb.ChatroomBcst_CustomContent{CustomContent: []byte{0, 1, 2}},
	}
	body, err := json.Marshal(ToPostData(in, allTypes))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"app_id":"app1","from":1,"from_member_info":{"uid":1,"nickname":"n","role_id":[1]},` +
		`"chatroom_id":9,"client_type":2,"msg_type":2,"custom_content":"AAEC","msg_id":"m1","create_time":123}`
	if string(body) != want {
		t.Fatalf("body =\n%s\nwant\n%s", body, want)
	}
}
