// Package watcher reacts to Redis key expirations: expiring mute and kick
// marks lift the restriction, and an expiring offline mark removes the user
// from every room.
package watcher

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/internal/chatroom/lock"
	"github.com/yourname/roomkit/internal/chatroom/logic"
	"github.com/yourname/roomkit/internal/chatroom/rediskey"
	"github.com/yourname/roomkit/internal/chatroom/reqctx"
	"github.com/yourname/roomkit/pkg/log"
)

const (
	notifyParam   = "notify-keyspace-events"
	handleTimeout = 30 * time.Second
)

// mergeNotifyFlags adds the flags needed for keyevent expiration
// notifications ("E" and "x") to the current setting. It reports whether
// the setting has to change.
func mergeNotifyFlags(cur string) (string, bool) {
	next := cur
	if !strings.ContainsRune(next, 'E') {
		next += "E"
	}
	if !strings.ContainsRune(next, 'x') && !strings.ContainsRune(next, 'A') {
		next += "x"
	}
	return next, next != cur
}

func expiredChannel(db int) string { return fmt.Sprintf("__keyevent@%d__:expired", db) }

// ensureNotify enables expiration events on one node. Managed Redis often
// forbids CONFIG, so failures are only logged; the setting must then be
// configured on the server.
func ensureNotify(ctx context.Context, node *redis.Client) {
	addr := node.Options().Addr
	cur := ""
	if res, err := node.ConfigGet(ctx, notifyParam).Result(); err != nil {
		log.L().Warn("redis CONFIG GET notify-keyspace-events failed", zap.String("addr", addr), zap.Error(err))
	} else {
		cur = res[notifyParam]
	}
	next, changed := mergeNotifyFlags(cur)
	if !changed {
		return
	}
	if err := node.ConfigSet(ctx, notifyParam, next).Err(); err != nil {
		log.L().Warn("redis CONFIG SET notify-keyspace-events failed; expiration events may be missing",
			zap.String("addr", addr), zap.String("want", next), zap.Error(err))
	}
}

// Run subscribes to expiration events of every master and handles them
// until ctx is done. Masters added to a cluster after Run starts are not
// subscribed. Every instance receives every event, as before.
func Run(ctx context.Context, rdb redis.UniversalClient) error {
	var wg sync.WaitGroup
	start := func(node *redis.Client, db int) {
		ensureNotify(ctx, node)
		ps := node.PSubscribe(ctx, expiredChannel(db))
		wg.Add(1)
		go func() {
			defer wg.Done()
			consume(ctx, ps)
		}()
	}

	switch c := rdb.(type) {
	case *redis.ClusterClient:
		if err := c.ForEachMaster(ctx, func(_ context.Context, node *redis.Client) error {
			start(node, 0)
			return nil
		}); err != nil {
			return fmt.Errorf("subscribe cluster masters: %w", err)
		}
	case *redis.Client:
		start(c, c.Options().DB)
	default:
		return fmt.Errorf("unsupported redis client %T", rdb)
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}

func consume(ctx context.Context, ps *redis.PubSub) {
	defer ps.Close()
	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			dispatch(msg.Payload)
		}
	}
}

func dispatch(key string) {
	if m, ok := rediskey.ParseKickedUser(key); ok {
		log.L().Debug("kicked mark expired", zap.String("key", key))
		go onKickExpired(m)
		return
	}
	if m, ok := rediskey.ParseMutedUser(key); ok {
		log.L().Debug("muted mark expired", zap.String("key", key))
		go onMuteExpired(m)
		return
	}
	if appID, uid, ok := rediskey.ParseUserOfflineChatroom(key); ok {
		log.L().Info("offline mark expired", zap.String("key", key))
		go onOfflineExpired(appID, uid)
	}
}

// withOpenRoom runs fn under the room process lock when the room exists and
// is open.
func withOpenRoom(m rediskey.RoomMember, name string, fn func(*reqctx.Context, *logic.ChatroomContainer)) {
	ctx, cancel := reqctx.WithTimeout(0, m.AppID, handleTimeout)
	defer cancel()
	if !lock.AcquireRoomProcess(ctx, m.AppID, m.RoomID) {
		ctx.Error(name+" get chatroom lock fail", zap.Int64("roomID", m.RoomID))
		return
	}
	defer lock.ReleaseRoomProcess(m.AppID, m.RoomID)

	room, err := logic.GetChatroom(ctx, m.AppID, m.RoomID)
	if err != nil {
		ctx.Error(name+" GetChatroom err", zap.Error(err))
		return
	}
	if errCode, err := room.BaseCheck(); err != nil {
		ctx.Error(name+" BaseCheck err", zap.Error(err))
		return
	} else if errCode != servicepb.ErrorCode_SUCCESS {
		return
	}
	fn(ctx, room)
}

func onKickExpired(m rediskey.RoomMember) {
	withOpenRoom(m, "kick expired", func(ctx *reqctx.Context, room *logic.ChatroomContainer) {
		errCode, info, err := room.GetChatroomMemberInfo(m.UID)
		if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
			ctx.Error("kick expired GetChatroomMemberInfo err", zap.Int("errCode", int(errCode)), zap.Error(err))
			return
		}
		if err := logic.UnkickMember(ctx, m.AppID, m.RoomID, m.UID); err != nil {
			ctx.Error("kick expired UnkickMember err", zap.Error(err))
		}
		if err := logic.BroadcastUnkicked(ctx, m.AppID, m.RoomID, m.UID, info); err != nil {
			ctx.Error("kick expired broadcast err", zap.Error(err))
		}
	})
}

func onMuteExpired(m rediskey.RoomMember) {
	withOpenRoom(m, "mute expired", func(ctx *reqctx.Context, room *logic.ChatroomContainer) {
		if err := logic.UnmuteMember(ctx, m.AppID, m.RoomID, m.UID); err != nil {
			ctx.Error("mute expired UnmuteMember err", zap.Error(err))
		}
		errCode, info, err := room.GetChatroomMemberInfo(m.UID)
		if err != nil || errCode != servicepb.ErrorCode_SUCCESS {
			ctx.Error("mute expired GetChatroomMemberInfo err", zap.Int("errCode", int(errCode)), zap.Error(err))
			return
		}
		if err := logic.BroadcastUnmuted(ctx, m.AppID, m.RoomID, m.UID, info); err != nil {
			ctx.Error("mute expired broadcast err", zap.Error(err))
		}
	})
}

func onOfflineExpired(appID string, uid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), handleTimeout)
	_ = logic.RemoveUserFromQueue(ctx, appID, uid)
	cancel()
	logic.BatchOutChatroom(appID, uid)
}
