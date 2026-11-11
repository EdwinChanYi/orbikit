package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/webhook/delivery"
)

type failingSource struct{}

func (failingSource) AppConfig(context.Context, string) (*delivery.AppConfig, error) {
	return nil, errors.New("core must not be called")
}

func TestAppInfoUpdatedRefreshesCache(t *testing.T) {
	apps := delivery.NewAppCache(failingSource{}, time.Minute, time.Minute)
	s := New(apps)
	reply, err := s.AppInfoUpdated(context.Background(), &servicepb.AppInfoUpdatedRequest{
		AppId: "a", AppSecret: "sec", MsgCpUrl: "m", TextCpUrl: "t", CustomCpUrl: "c",
		Types: "1,3", Qps: 1, TextQps: 2, CustomQps: 3,
	})
	if err != nil || reply.GetRet().GetErrorCode() != servicepb.ErrorCode_SUCCESS {
		t.Fatalf("reply = %v, %v", reply, err)
	}
	cfg, err := apps.Get(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Secret != "sec" || cfg.MsgCpURL != "m" || cfg.TextCpURL != "t" || cfg.CustomCpURL != "c" ||
		len(cfg.Types) != 2 || cfg.QPS != 1 || cfg.TextQPS != 2 || cfg.CustomQPS != 3 {
		t.Fatalf("cached config = %+v", cfg)
	}
}
