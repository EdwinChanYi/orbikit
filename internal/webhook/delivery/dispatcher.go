package delivery

import (
	"context"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/pkg/log"
)

// HandleFunc processes one broadcast.
type HandleFunc func(ctx context.Context, msg *servicepb.ChatroomBcst)

// Dispatcher queues broadcasts per app and processes each app's queue with its
// own workers, so a slow or rate limited app cannot hold up the others. Queues
// are bounded: Submit never blocks and drops the message when the app's queue
// is full.
type Dispatcher struct {
	workers   int
	queueSize int
	handle    HandleFunc

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.RWMutex
	closed bool
	queues map[string]chan *servicepb.ChatroomBcst

	dropped atomic.Uint64
}

// NewDispatcher returns a Dispatcher running workersPerApp workers per app.
func NewDispatcher(workersPerApp, queueSize int, handle HandleFunc) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		workers:   max(workersPerApp, 1),
		queueSize: max(queueSize, 1),
		handle:    handle,
		ctx:       ctx,
		cancel:    cancel,
		queues:    make(map[string]chan *servicepb.ChatroomBcst),
	}
}

// Submit enqueues msg. It reports false when the message was dropped because
// the queue is full or the dispatcher is closed.
func (d *Dispatcher) Submit(msg *servicepb.ChatroomBcst) bool {
	appID := msg.GetAppId()

	d.mu.RLock()
	q, ok := d.queues[appID]
	if ok && !d.closed {
		accepted := d.offer(q, msg)
		d.mu.RUnlock()
		return accepted
	}
	d.mu.RUnlock()

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}
	q, ok = d.queues[appID]
	if !ok {
		q = make(chan *servicepb.ChatroomBcst, d.queueSize)
		d.queues[appID] = q
		for i := 0; i < d.workers; i++ {
			d.wg.Add(1)
			go d.work(q)
		}
	}
	return d.offer(q, msg)
}

func (d *Dispatcher) offer(q chan *servicepb.ChatroomBcst, msg *servicepb.ChatroomBcst) bool {
	select {
	case q <- msg:
		return true
	default:
		n := d.dropped.Add(1)
		log.L().Warn("webhook queue full, message dropped",
			zap.String("app_id", msg.GetAppId()),
			zap.String("msg_id", msg.GetMsgId()),
			zap.Uint64("dropped_total", n))
		return false
	}
}

func (d *Dispatcher) work(q <-chan *servicepb.ChatroomBcst) {
	defer d.wg.Done()
	for msg := range q {
		d.handle(d.ctx, msg)
	}
}

// Dropped returns the number of messages dropped so far.
func (d *Dispatcher) Dropped() uint64 { return d.dropped.Load() }

// Close stops accepting messages and lets workers drain the queues. When ctx
// is done first, in-flight deliveries are cancelled and remaining messages are
// discarded. Close waits for all workers to exit.
func (d *Dispatcher) Close(ctx context.Context) {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		for _, q := range d.queues {
			close(q)
		}
	}
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		d.cancel()
		<-done
	}
	d.cancel()
}
