package delivery

import (
	"context"
	"math"
	"sync"

	"golang.org/x/time/rate"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

// Limiters keeps one token bucket per (app, message type). It is safe for
// concurrent use by any number of workers.
type Limiters struct {
	mu sync.Mutex
	m  map[limiterKey]*limiterEntry
}

type limiterKey struct {
	appID   string
	msgType servicepb.ChatroomMsgType
}

type limiterEntry struct {
	qps int64
	lim *rate.Limiter
}

// NewLimiters returns an empty limiter set.
func NewLimiters() *Limiters {
	return &Limiters{m: make(map[limiterKey]*limiterEntry)}
}

// Wait blocks until a message of msgType for appID may be sent under qps
// messages per second, or ctx is done. qps <= 0 means unlimited. A changed qps
// for the same key takes effect immediately.
func (l *Limiters) Wait(ctx context.Context, appID string, msgType servicepb.ChatroomMsgType, qps int64) error {
	if qps <= 0 {
		return ctx.Err()
	}
	return l.limiter(appID, msgType, qps).Wait(ctx)
}

func (l *Limiters) limiter(appID string, msgType servicepb.ChatroomMsgType, qps int64) *rate.Limiter {
	burst := int(min(qps, math.MaxInt32))
	key := limiterKey{appID: appID, msgType: msgType}

	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[key]
	if !ok {
		e = &limiterEntry{qps: qps, lim: rate.NewLimiter(rate.Limit(qps), burst)}
		l.m[key] = e
	} else if e.qps != qps {
		e.qps = qps
		e.lim.SetLimit(rate.Limit(qps))
		e.lim.SetBurst(burst)
	}
	return e.lim
}
