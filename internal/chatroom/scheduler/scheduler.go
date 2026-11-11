// Package scheduler drives the periodic after-deal work: refreshing room
// ranks and removing users whose offline grace period has elapsed.
package scheduler

import (
	"context"
	"time"

	"github.com/yourname/roomkit/internal/chatroom/logic"
)

const interval = time.Second

// Run processes the after-deal queues every second until ctx is done.
func Run(ctx context.Context) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			logic.QueueDeal(ctx)
			logic.DealUserQueue(ctx)
		}
	}
}
