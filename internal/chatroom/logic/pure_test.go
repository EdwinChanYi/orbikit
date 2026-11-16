package logic

import (
	"strings"
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/model"
)

func TestMicSeatsRoundTrip(t *testing.T) {
	seats := map[int32]*MicSpaceElementInfo{
		0: {Index: 0, HasLock: true},
		3: {Index: 3, HasUser: true, Uid: 42, Ext: map[string]string{"k": "v"}},
	}
	fields := map[string]any{micFieldAppID: "app", micFieldRoomID: "7"}
	if err := encodeMicSeats(fields, seats); err != nil {
		t.Fatal(err)
	}
	data := make(map[string]string, len(fields))
	for k, v := range fields {
		data[k] = v.(string)
	}
	got, err := decodeMicSpace(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].HasLock || got[3].Uid != 42 || got[3].Ext["k"] != "v" {
		t.Fatalf("decoded seats = %+v", got)
	}
}

func TestDecodeMicSpaceRejectsBadFields(t *testing.T) {
	if _, err := decodeMicSpace(map[string]string{"x": "{}"}); err == nil {
		t.Fatal("non-numeric seat field should fail")
	}
	if _, err := decodeMicSpace(map[string]string{"1": "not json"}); err == nil {
		t.Fatal("malformed seat JSON should fail")
	}
}

func TestSeatsByUser(t *testing.T) {
	got := seatsByUser(map[int32]*MicSpaceElementInfo{
		1: {Index: 1, Uid: 10},
		2: {Index: 2},
		5: {Index: 5, Uid: 20},
	})
	if len(got) != 2 || got[10] != 1 || got[20] != 5 {
		t.Fatalf("seatsByUser = %v", got)
	}
}

func TestApplyMicExt(t *testing.T) {
	seats := map[int32]*MicSpaceElementInfo{
		1: {Index: 1, Ext: map[string]string{"a": "1", "b": "2"}},
		2: {Index: 2, Ext: map[string]string{"x": "1"}},
		3: {Index: 3},
	}
	ok := applyMicExt(seats, []*servicepb.SpaceInfosExt{
		{Index: 1, Ext: map[string]string{"a": "", "c": "3"}},
		{Index: 2},
		{Index: 3, Ext: map[string]string{"n": "new"}},
	})
	if !ok {
		t.Fatal("applyMicExt reported a missing seat")
	}
	if e := seats[1].Ext; len(e) != 2 || e["b"] != "2" || e["c"] != "3" {
		t.Fatalf("seat 1 ext = %v, want b and c", e)
	}
	if seats[2].Ext != nil {
		t.Fatalf("empty update should clear seat 2 ext, got %v", seats[2].Ext)
	}
	if seats[3].Ext["n"] != "new" {
		t.Fatalf("seat 3 ext = %v", seats[3].Ext)
	}
	if applyMicExt(seats, []*servicepb.SpaceInfosExt{{Index: 9}}) {
		t.Fatal("unknown seat should be reported")
	}
}

func TestPickPermission(t *testing.T) {
	if pickPermission(nil, true) != nil {
		t.Fatal("no permissions should yield nil")
	}
	perms := []model.Permission{
		{RoleId: 1, InitiativeLevel: 5, PassivityLevel: 2, CanDoAction: true},
		{RoleId: 2, InitiativeLevel: 3, PassivityLevel: 4, CanDoAction: false},
		{RoleId: 3, InitiativeLevel: 1, PassivityLevel: 1, CanDoAction: true},
	}
	// The current pick is only replaced while it can perform the action, so
	// a role that cannot act stops the search for a stronger initiative role.
	if got := pickPermission(perms, true); got.RoleId != 2 {
		t.Fatalf("initiative pick = role %d, want 2", got.RoleId)
	}
	if got := pickPermission(perms, false); got.RoleId != 3 {
		t.Fatalf("passivity pick = role %d, want 3", got.RoleId)
	}
}

func TestMemberPage(t *testing.T) {
	asc := servicepb.ChatroomMemberFetchSortType_CHATROOM_MEMBER_FETCH_SORT_ASC

	p := newMemberPage(asc, 0, 0)
	if p.limit != defaultMemberLimit {
		t.Fatalf("default limit = %d", p.limit)
	}
	if cond, _ := p.cursor(); cond != "" {
		t.Fatalf("first page should have no cursor, got %q", cond)
	}
	if got := p.order(true); got != "ORDER BY mi.is_robot ASC, mi.updated_at ASC LIMIT 20" {
		t.Fatalf("order = %q", got)
	}

	enter := time.Date(2024, 5, 6, 7, 8, 9, 500*int(time.Millisecond), time.Local).UnixMilli()
	cond, v := newMemberPage(asc, enter, 5).cursor()
	if !strings.Contains(cond, ">") {
		t.Fatalf("ascending cursor = %q", cond)
	}
	if got, _ := model.ParseTime(v.(string)); got.UnixMilli() != enter+1 {
		t.Fatalf("ascending cursor value %v, want %d ms", v, enter+1)
	}

	desc := newMemberPage(servicepb.ChatroomMemberFetchSortType(0), enter, 5)
	sqlText, args := desc.apply("SELECT 1 ", []any{"app"}, false)
	if !strings.Contains(sqlText, "mi.updated_at < ?") || !strings.HasSuffix(sqlText, "ORDER BY mi.updated_at DESC LIMIT 5") {
		t.Fatalf("descending sql = %q", sqlText)
	}
	if len(args) != 2 {
		t.Fatalf("args = %v", args)
	}
	if got, _ := model.ParseTime(args[1].(string)); got.UnixMilli() != enter-1 {
		t.Fatalf("descending cursor value %v, want %d ms", args[1], enter-1)
	}
}

func TestParseMemberRow(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	ts := model.FormatTime(now)
	row := map[string]string{
		"uid": "7", "chatroom_id": "9", "app_id": "app", "nickname": "n",
		"custom_info": `{"a":"b"}`, "chatroom_remote_ext": "", "search_data": "null",
		"is_robot": "0", "is_block": "1", "is_muted": "0", "is_kicked": "0", "is_online": "1",
		"muted_begin_at": ts, "muted_finish_at": ts, "kicked_begin_at": ts, "kicked_finish_at": ts,
		"updated_at": ts,
	}
	m, err := parseMemberRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if m.Uid != 7 || m.ChatroomId != 9 || m.CustomInfo["a"] != "b" || m.ChatroomRemoteExt != nil {
		t.Fatalf("parsed row = %+v", m)
	}
	if m.IsRobot || !m.IsBlock || !m.IsOnline || !m.UpdatedAt.Equal(now) {
		t.Fatalf("parsed flags/time = %+v", m)
	}
	if got := m.Proto().GetEnterTime(); got != now.UnixMilli() {
		t.Fatalf("EnterTime = %d, want %d", got, now.UnixMilli())
	}

	row["custom_info"] = "{"
	if _, err := parseMemberRow(row); err == nil {
		t.Fatal("malformed custom_info should fail")
	}
}

func TestJoinRoleIDs(t *testing.T) {
	in := []int64{30, 1, 20}
	if got := joinRoleIDs(in); got != "1,20,30" {
		t.Fatalf("joinRoleIDs = %q", got)
	}
	if in[0] != 30 {
		t.Fatal("joinRoleIDs must not reorder its input")
	}
	if got := joinRoleIDs(nil); got != "" {
		t.Fatalf("joinRoleIDs(nil) = %q", got)
	}
}

func TestRandString(t *testing.T) {
	s, err := randString(enterTokenLen, enterTokenLetters)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != enterTokenLen {
		t.Fatalf("len = %d", len(s))
	}
	for _, r := range s {
		if !strings.ContainsRune(enterTokenLetters, r) {
			t.Fatalf("unexpected rune %q", r)
		}
	}
	other, _ := randString(enterTokenLen, enterTokenLetters)
	if other == s {
		t.Fatal("two tokens should differ")
	}
}

func TestEmptyText(t *testing.T) {
	text := servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT
	custom := servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM
	if !EmptyText(text, "") || !EmptyText(text, "  \n") {
		t.Fatal("blank text should be rejected")
	}
	if EmptyText(text, "hi") || EmptyText(custom, "") {
		t.Fatal("non-blank text and custom messages should be kept")
	}
}

func TestFilterCustomInfo(t *testing.T) {
	info := map[string]string{"nick": "a", "city": "b"}
	if got := FilterCustomInfo(info, []string{"nick"}, false); len(got) != 2 || got["city"] != "b" {
		t.Fatalf("unconfigured filter = %v", got)
	}
	got := FilterCustomInfo(info, []string{"nick", "missing"}, true)
	if len(got) != 1 || got["nick"] != "a" {
		t.Fatalf("configured filter = %v", got)
	}
	if got := FilterCustomInfo(info, nil, true); len(got) != 0 {
		t.Fatalf("empty filter = %v", got)
	}
}

func TestKickedReason(t *testing.T) {
	at := time.Unix(1, 5)
	if got := kickedReason(at, 3, servicepb.ChatroomKickReason(2)); got != "1000000005,3,2" {
		t.Fatalf("kickedReason = %q", got)
	}
}
