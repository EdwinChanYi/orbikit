package rediskey

import "testing"

func TestParseMemberKeysRoundTrip(t *testing.T) {
	want := RoomMember{AppID: "app-1", RoomID: 1001, UID: 42}
	if got, ok := ParseMutedUser(ChatroomMutedUser(want.AppID, want.RoomID, want.UID)); !ok || got != want {
		t.Fatalf("ParseMutedUser = %+v, %v", got, ok)
	}
	if got, ok := ParseKickedUser(ChatroomKickedUser(want.AppID, want.RoomID, want.UID)); !ok || got != want {
		t.Fatalf("ParseKickedUser = %+v, %v", got, ok)
	}
	appID, uid, ok := ParseUserOfflineChatroom(UserOfflineChatroom("app-1", 42))
	if !ok || appID != "app-1" || uid != 42 {
		t.Fatalf("ParseUserOfflineChatroom = %q, %d, %v", appID, uid, ok)
	}
}

func TestParseMemberKeysReject(t *testing.T) {
	for _, key := range []string{
		"",
		ChatroomKickedUser("app", 1, 2),
		"CHATROOM_SERVICE:MUTED_USER:APP_ID::ROOM_ID:1:UID:2",
		"CHATROOM_SERVICE:MUTED_USER:APP_ID:a:ROOM_ID:x:UID:2",
		"CHATROOM_SERVICE:MUTED_USER:APP_ID:a:ROOM_ID:1:UID:2:extra",
		"CHATROOM_SERVICE:MUTED_USER:APP_ID:a:ROOM:1:UID:2",
	} {
		if _, ok := ParseMutedUser(key); ok {
			t.Errorf("ParseMutedUser(%q) should fail", key)
		}
	}
	for _, key := range []string{
		ChatroomMutedUser("app", 1, 2),
		"CHATROOM_SERVICE:OFFLINEONLINECHATROOM:APP_ID:a:UID:",
		"CHATROOM_SERVICE:OFFLINEONLINECHATROOM:APP_ID:a:UID:1:x",
	} {
		if _, _, ok := ParseUserOfflineChatroom(key); ok {
			t.Errorf("ParseUserOfflineChatroom(%q) should fail", key)
		}
	}
}

func TestQueueMember(t *testing.T) {
	v := QueueMember("my_app", 77)
	if v != "my_app_77" {
		t.Fatalf("QueueMember = %q", v)
	}
	appID, id, ok := ParseQueueMember(v)
	if !ok || appID != "my_app" || id != 77 {
		t.Fatalf("ParseQueueMember = %q, %d, %v", appID, id, ok)
	}
	for _, bad := range []string{"", "_1", "app_", "app_x", "noid"} {
		if _, _, ok := ParseQueueMember(bad); ok {
			t.Errorf("ParseQueueMember(%q) should fail", bad)
		}
	}
}

func TestSharedKeyFormats(t *testing.T) {
	tests := map[string]string{
		ChatroomInfo("a", 1):              "CHATROOM_SERVICE:CHATROOM_INFO:APP_ID:a:ROOM_ID:1",
		ChatroomOnlineMember("a", 1):      "CHATROOM_SERVICE:CHATROOM_ONLINE_MEMBER:APP_ID:a:ROOM_ID:1",
		MemberInChatroomSet("a", 2):       "CHATROOM_SERVICE:MEMBER_IN_CHATROOM_SET:APP_ID:a:UID:2",
		ChatroomUserLogicRole("a", 1, 2):  "CHATROOM_SERVICE:USER_LOGIC_ROLE:a:1:2",
		ChatroomUsedMicSpaceCount("a", 1): "CHATROOM_SERVICE:CHATROOM_USED_MIC_SPACE_RANK:APP_ID:a:ROOM_ID:1",
		ChatroomOnlineRealMember("a", 1):  "CROnlineRMema:1",
		ChatroomProcessLock("a", 1):       "CHATROOM_TEMP_LOCKER:a:1",
	}
	for got, want := range tests {
		if got != want {
			t.Errorf("key = %q, want %q", got, want)
		}
	}
}
