// Package reqctx carries a per-request message ID and logger through the
// chatroom logic layer.
package reqctx

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/pkg/log"
)

var (
	nextID     atomic.Pointer[func() uint64]
	traceReply atomic.Bool
)

// SetIDGenerator installs the generator used when a request carries no message ID.
func SetIDGenerator(fn func() uint64) { nextID.Store(&fn) }

// SetTraceReply enables logging full gRPC replies at debug level.
func SetTraceReply(on bool) { traceReply.Store(on) }

// NewID returns a fresh message ID, or 0 if no generator is installed.
func NewID() uint64 {
	if fn := nextID.Load(); fn != nil {
		return (*fn)()
	}
	return 0
}

// Context is a context.Context with a message ID and request-scoped logger.
type Context struct {
	context.Context
	msgID uint64
	appID string
	begin time.Time
	log   *zap.Logger
}

// New wraps parent. A zero msgID is replaced with a generated one.
func New(parent context.Context, msgID uint64, appID string) *Context {
	if msgID == 0 {
		msgID = NewID()
	}
	return &Context{
		Context: parent,
		msgID:   msgID,
		appID:   appID,
		begin:   time.Now(),
		log: log.L().WithOptions(zap.AddCallerSkip(1)).With(
			zap.String("app_id", appID),
			zap.Uint64("msg_id", msgID),
		),
	}
}

// WithTimeout creates a background Context that expires after d.
func WithTimeout(msgID uint64, appID string, d time.Duration) (*Context, context.CancelFunc) {
	parent, cancel := context.WithTimeout(context.Background(), d)
	return New(parent, msgID, appID), cancel
}

// MsgID returns the request message ID.
func (c *Context) MsgID() uint64 { return c.msgID }

// AppID returns the request app ID.
func (c *Context) AppID() string { return c.appID }

func (c *Context) Debug(msg string, fields ...zap.Field) { c.log.Debug(msg, fields...) }
func (c *Context) Info(msg string, fields ...zap.Field)  { c.log.Info(msg, fields...) }
func (c *Context) Warn(msg string, fields ...zap.Field)  { c.log.Warn(msg, fields...) }
func (c *Context) Error(msg string, fields ...zap.Field) { c.log.Error(msg, fields...) }

// TraceReply logs the outcome of a gRPC call.
func (c *Context) TraceReply(reply any, err error) {
	used := zap.Duration("used_time", time.Since(c.begin))
	switch {
	case err != nil:
		c.log.Error("call service error reply catch", zap.Error(err), used)
	case traceReply.Load():
		c.log.Debug("call service reply trace", zap.Any("reply", reply), used)
	default:
		c.log.Debug("call service reply trace", used)
	}
}
