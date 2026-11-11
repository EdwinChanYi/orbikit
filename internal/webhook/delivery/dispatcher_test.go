package delivery

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"google.golang.org/protobuf/proto"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

func bcst(appID string) *servicepb.ChatroomBcst {
	return &servicepb.ChatroomBcst{AppId: appID, MsgType: text, Content: &servicepb.ChatroomBcst_TextContent{TextContent: "x"}}
}

func TestDispatcherProcessesAll(t *testing.T) {
	var mu sync.Mutex
	got := map[string]int{}
	d := NewDispatcher(4, 100, func(_ context.Context, m *servicepb.ChatroomBcst) {
		mu.Lock()
		got[m.GetAppId()]++
		mu.Unlock()
	})
	for i := 0; i < 50; i++ {
		d.Submit(bcst("a"))
		d.Submit(bcst("b"))
	}
	d.Close(context.Background())
	if got["a"] != 50 || got["b"] != 50 {
		t.Fatalf("processed %v", got)
	}
	if d.Submit(bcst("a")) {
		t.Fatal("Submit after Close must be rejected")
	}
}

func TestDispatcherIsolatesApps(t *testing.T) {
	block := make(chan struct{})
	var bDone atomic.Int32
	d := NewDispatcher(1, 10, func(ctx context.Context, m *servicepb.ChatroomBcst) {
		if m.GetAppId() == "slow" {
			select {
			case <-block:
			case <-ctx.Done():
			}
			return
		}
		bDone.Add(1)
	})
	d.Submit(bcst("slow"))
	for i := 0; i < 5; i++ {
		d.Submit(bcst("fast"))
	}
	deadline := time.Now().Add(time.Second)
	for bDone.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if bDone.Load() != 5 {
		t.Fatalf("fast app processed %d/5 while slow app blocked", bDone.Load())
	}
	close(block)
	d.Close(context.Background())
}

func TestDispatcherDropsWhenFull(t *testing.T) {
	block := make(chan struct{})
	d := NewDispatcher(1, 2, func(context.Context, *servicepb.ChatroomBcst) { <-block })
	accepted := 0
	for i := 0; i < 10; i++ {
		if d.Submit(bcst("a")) {
			accepted++
		}
	}
	// One in the worker plus two queued at most; never blocks.
	if accepted > 3 || accepted < 2 || d.Dropped() != uint64(10-accepted) {
		t.Fatalf("accepted %d dropped %d", accepted, d.Dropped())
	}
	close(block)
	d.Close(context.Background())
}

func TestDispatcherCloseTimeoutCancels(t *testing.T) {
	var cancelled atomic.Bool
	d := NewDispatcher(1, 10, func(ctx context.Context, _ *servicepb.ChatroomBcst) {
		<-ctx.Done()
		cancelled.Store(true)
	})
	d.Submit(bcst("a"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { d.Close(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after its deadline")
	}
	if !cancelled.Load() {
		t.Fatal("in-flight handler should observe cancellation")
	}
}

func TestKafkaHandler(t *testing.T) {
	var got atomic.Int32
	d := NewDispatcher(1, 10, func(context.Context, *servicepb.ChatroomBcst) { got.Add(1) })
	h := KafkaHandler(d)

	raw, _ := proto.Marshal(bcst("a"))
	if err := h(context.Background(), &sarama.ConsumerMessage{Value: raw}); err != nil {
		t.Fatal(err)
	}
	noApp, _ := proto.Marshal(&servicepb.ChatroomBcst{MsgType: text})
	if err := h(context.Background(), &sarama.ConsumerMessage{Value: noApp}); err != nil {
		t.Fatal(err)
	}
	if err := h(context.Background(), &sarama.ConsumerMessage{Value: []byte{0xff, 0xff}}); err == nil {
		t.Fatal("garbage must be reported")
	}
	d.Close(context.Background())
	if got.Load() != 1 {
		t.Fatalf("handled %d, want 1 (messages without app id are skipped)", got.Load())
	}
}

// TestCopierEndToEnd runs a broadcast through cache, conversion, rate limiter
// and signed HTTP delivery against a fake receiver.
func TestCopierEndToEnd(t *testing.T) {
	type hit struct {
		path string
		body map[string]any
		ok   bool
	}
	hits := make(chan hit, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sum := fmt.Sprintf("%x", sha1.Sum([]byte("sec"+r.Header.Get("nonce")+r.Header.Get("cur-time")+string(raw))))
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		hits <- hit{path: r.URL.Path, body: body, ok: sum == r.Header.Get("check-sum") && r.Header.Get("app-id") == "app1"}
	}))
	defer srv.Close()

	src := &fakeSource{cfg: map[string]*AppConfig{
		"app1":   {Secret: "sec", TextCpURL: srv.URL + "/text", CustomCpURL: "", Types: []int32{255}, TextQPS: 100},
		"nosec":  {Secret: "", TextCpURL: srv.URL + "/text", Types: []int32{255}},
		"notext": {Secret: "sec", TextCpURL: srv.URL + "/text", Types: []int32{2}},
	}}
	c := NewCopier(NewAppCache(src, time.Minute, time.Minute), NewLimiters(), testSender(0))

	ctx := context.Background()
	c.Handle(ctx, bcst("app1"))
	c.Handle(ctx, &servicepb.ChatroomBcst{AppId: "app1", MsgType: custom,
		Content: &servicepb.ChatroomBcst_CustomContent{CustomContent: []byte("x")}})
	c.Handle(ctx, bcst("nosec"))
	c.Handle(ctx, bcst("notext"))
	c.Handle(ctx, bcst("unknown"))

	select {
	case h := <-hits:
		if h.path != "/text" || !h.ok || h.body["text_content"] != "x" || h.body["app_id"] != "app1" {
			t.Fatalf("unexpected delivery %+v", h)
		}
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
	select {
	case h := <-hits:
		t.Fatalf("unexpected extra delivery %+v", h)
	default:
	}
}
