package appnotify

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
)

func TestPublishReachesSubscriber(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	bus := New(rdb, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan *servicepb.AppInfoUpdatedRequest, 4)
	go bus.Run(ctx, func(req *servicepb.AppInfoUpdatedRequest) { got <- req })

	want := &servicepb.AppInfoUpdatedRequest{AppId: "app-1", AppSecret: "secret", TextCpUrl: "https://example.test/text", TextQps: 10}
	deadline := time.Now().Add(3 * time.Second)
	for {
		bus.Publish(context.Background(), want)
		select {
		case req := <-got:
			if req.GetAppId() != want.AppId || req.GetAppSecret() != want.AppSecret || req.GetTextCpUrl() != want.TextCpUrl || req.GetTextQps() != want.TextQps {
				t.Fatalf("event = %+v", req)
			}
			return
		case <-time.After(30 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("subscriber received nothing")
			}
		}
	}
}

func TestBadPayloadIsSkipped(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	bus := New(rdb, Config{Channel: "custom"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 2)
	go bus.Run(ctx, func(req *servicepb.AppInfoUpdatedRequest) { got <- req.GetAppId() })

	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = rdb.Publish(context.Background(), "custom", "not-protobuf").Err()
		bus.Publish(context.Background(), &servicepb.AppInfoUpdatedRequest{AppId: "ok"})
		select {
		case id := <-got:
			if id == "ok" {
				return
			}
		case <-time.After(30 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("valid event never arrived")
			}
		}
	}
}
