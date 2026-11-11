package delivery

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

const (
	text   = servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_TEXT
	custom = servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_CUSTOM
	event  = servicepb.ChatroomMsgType_CHATROOM_MSG_TYPE_EVENT
)

func TestLimiterUnlimited(t *testing.T) {
	l := NewLimiters()
	start := time.Now()
	for i := 0; i < 10000; i++ {
		for _, qps := range []int64{0, -1} {
			if err := l.Wait(context.Background(), "a", text, qps); err != nil {
				t.Fatal(err)
			}
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("unlimited waits took %v", d)
	}
	if len(l.m) != 0 {
		t.Fatal("no limiter should be created for qps <= 0")
	}
}

func TestLimiterEnforcesQPS(t *testing.T) {
	l := NewLimiters()
	ctx := context.Background()
	const qps = 20

	start := time.Now()
	for i := 0; i < qps; i++ { // the burst passes immediately
		if err := l.Wait(ctx, "a", text, qps); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("burst of %d took %v", qps, d)
	}

	start = time.Now()
	for i := 0; i < 5; i++ { // the next 5 need ~5/20s
		if err := l.Wait(ctx, "a", text, qps); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Fatalf("5 requests over the limit took only %v, want >= ~250ms", d)
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := NewLimiters()
	ctx := context.Background()
	// Exhaust app a / text.
	for i := 0; i < 5; i++ {
		_ = l.Wait(ctx, "a", text, 5)
	}
	start := time.Now()
	for _, k := range []struct {
		app string
		typ servicepb.ChatroomMsgType
	}{{"a", custom}, {"a", event}, {"b", text}} {
		for i := 0; i < 5; i++ {
			if err := l.Wait(ctx, k.app, k.typ, 5); err != nil {
				t.Fatal(err)
			}
		}
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("other keys were throttled by app a/text: %v", d)
	}
}

func TestLimiterContextCancel(t *testing.T) {
	l := NewLimiters()
	_ = l.Wait(context.Background(), "a", text, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx, "a", text, 1); err == nil {
		t.Fatal("wait beyond the deadline must fail")
	}
}

func TestLimiterQPSUpdate(t *testing.T) {
	l := NewLimiters()
	ctx := context.Background()
	_ = l.Wait(ctx, "a", text, 1)
	if e := l.m[limiterKey{"a", text}]; e.lim.Burst() != 1 {
		t.Fatalf("burst = %d", e.lim.Burst())
	}
	_ = l.Wait(ctx, "a", text, 1000)
	e := l.m[limiterKey{"a", text}]
	if e.qps != 1000 || e.lim.Burst() != 1000 || float64(e.lim.Limit()) != 1000 {
		t.Fatalf("limiter not updated: qps=%d burst=%d limit=%v", e.qps, e.lim.Burst(), e.lim.Limit())
	}
}

// TestLimiterConcurrent hammers one key from many goroutines; run with -race.
// Across all workers the total rate must stay within burst + qps*elapsed.
func TestLimiterConcurrent(t *testing.T) {
	l := NewLimiters()
	const qps = 50
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	var passed atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for l.Wait(ctx, "a", event, qps) == nil {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	limit := int64(qps + qps*elapsed + 2)
	if got := passed.Load(); got > limit || got < qps {
		t.Fatalf("passed %d in %.2fs, want between %d and %d", got, elapsed, qps, limit)
	}
}
