package push

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/yourname/roomkit/internal/gateway/session"
	"github.com/yourname/roomkit/internal/gateway/store"
	"github.com/yourname/roomkit/pkg/log"
)

// Kicker removes a session that was signed in elsewhere.
type Kicker interface {
	Kick(s *session.Session)
}

// KickWatcher enforces one connection per user across gateways: every AUTH
// publishes a new-connection event and the gateway holding the previous
// connection drops it.
type KickWatcher struct {
	rdb       redis.UniversalClient
	store     *store.Store
	sessions  *session.Manager
	kicker    Kicker
	machineID uint16
}

// NewKickWatcher returns a watcher for this gateway instance.
func NewKickWatcher(st *store.Store, sessions *session.Manager, kicker Kicker, machineID uint16) *KickWatcher {
	return &KickWatcher{rdb: st.Redis(), store: st, sessions: sessions, kicker: kicker, machineID: machineID}
}

// Run consumes events until ctx is cancelled.
func (w *KickWatcher) Run(ctx context.Context) {
	ps := w.rdb.Subscribe(ctx, store.NewConnectionChannel)
	defer ps.Close()
	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			w.handle(ctx, m.Payload)
		}
	}
}

func (w *KickWatcher) handle(ctx context.Context, payload string) {
	machineID, connID, appID, uid, err := store.ParseNewConnectionEvent(payload)
	if err != nil {
		log.L().Warn("bad new connection event", zap.Error(err))
		return
	}
	s := w.sessions.Lookup(appID, uid)
	if s == nil {
		return
	}
	if machineID == w.machineID && s.ID() == connID {
		// The announcing connection is already the bound one, so nothing
		// else holds the link and the waiting login must not stall.
		cctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := w.store.ClearLinkStatus(cctx, appID, uid); err != nil {
			log.L().Warn("clear link status", zap.Error(err))
		}
		return
	}
	log.L().Info("kick previous connection",
		zap.String("app_id", appID),
		zap.Int64("uid", uid),
		zap.Uint64("old_conn_id", s.ID()),
		zap.Uint16("new_machine_id", machineID),
		zap.Uint64("new_conn_id", connID))
	w.kicker.Kick(s)
}
