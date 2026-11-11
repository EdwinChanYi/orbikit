//go:build redis_integration

// Run with a disposable Redis:
//
//	ORBIKIT_TEST_REDIS=localhost:6379 go test -tags redis_integration ./internal/chatroom/logic/
package logic

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/chatroom/rediskey"
)

func setupRankRedis(t *testing.T) (context.Context, string) {
	t.Helper()
	addr := os.Getenv("ORBIKIT_TEST_REDIS")
	if addr == "" {
		t.Skip("ORBIKIT_TEST_REDIS is not set")
	}
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	if err := c.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	rdb = c
	appID := "rank-test-" + t.Name()

	type room struct {
		id          int64
		members     int64
		robots      int64
		usedMicSeat int64
	}
	rooms := []room{{1, 10, 2, 1}, {2, 30, 0, 3}, {3, 20, 1, 8}}
	var keys []string
	for _, r := range rooms {
		onlineKey := rediskey.ChatroomOnlineCount(appID, r.id)
		robotKey := rediskey.ChatroomRobotCount(appID, r.id)
		micKey := rediskey.ChatroomUsedMicSpaceCount(appID, r.id)
		keys = append(keys, onlineKey, robotKey, micKey)
		c.Set(ctx, onlineKey, r.members, 0)
		c.Set(ctx, robotKey, r.robots, 0)
		c.Set(ctx, micKey, r.usedMicSeat, 0)
		if err := updateMemmicZ(ctx, appID, r.id); err != nil {
			t.Fatal(err)
		}
		if err := updateMicmemZ(ctx, appID, r.id); err != nil {
			t.Fatal(err)
		}
	}
	keys = append(keys, rediskey.ChatroomOnlineMemberRank(appID), rediskey.ChatroomUsedMicSpaceRank(appID))
	t.Cleanup(func() {
		c.Del(context.Background(), keys...)
		_ = c.Close()
	})
	return ctx, appID
}

func roomIDsOf(infos []*servicepb.ChatroomMemberCountInfo) []int64 {
	ids := make([]int64, len(infos))
	for i, info := range infos {
		ids[i] = info.GetChatroomId()
	}
	return ids
}

func TestGetSortRoomByMembersRedis(t *testing.T) {
	ctx, appID := setupRankRedis(t)
	code, infos, err := GetSortRoomByMembers(ctx, appID, true, 1, 2)
	if err != nil || code != servicepb.ErrorCode_SUCCESS {
		t.Fatalf("code=%v err=%v", code, err)
	}
	if got := roomIDsOf(infos); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("rooms = %v, want [2 3]", got)
	}
	if infos[0].GetOnlineCount() != 30 || infos[1].GetRobotCount() != 1 {
		t.Fatalf("unexpected counts: %+v", infos)
	}

	_, infos, _ = GetSortRoomByMembers(ctx, appID, false, 1, 5)
	if got := roomIDsOf(infos); len(got) != 3 || got[0] != 1 {
		t.Fatalf("ascending rooms = %v, want room 1 first", got)
	}
}

func TestGetSortRoomByUsedMicCountRedis(t *testing.T) {
	ctx, appID := setupRankRedis(t)
	code, infos, err := GetSortRoomByUsedMicCount(ctx, appID, true, 1, 5)
	if err != nil || code != servicepb.ErrorCode_SUCCESS {
		t.Fatalf("code=%v err=%v", code, err)
	}
	if got := roomIDsOf(infos); len(got) != 3 || got[0] != 3 || got[2] != 1 {
		t.Fatalf("rooms = %v, want [3 2 1]", got)
	}
	if infos[0].GetOnlineCount() != 8 {
		t.Fatalf("OnlineCount carries the used mic count, got %d", infos[0].GetOnlineCount())
	}
}
