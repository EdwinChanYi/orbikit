package server

import (
	"testing"

	"github.com/EdwinChanYi/orbikit/api/gen/servicepb"
	"github.com/EdwinChanYi/orbikit/internal/core/store"
)

func TestNormalizeDupli(t *testing.T) {
	cases := []struct{ dupli, expire, wantDupli, wantExpire int32 }{
		{0, 10, 0, 10},
		{1, 10, 3, 10},
		{2, -5, 3, 0},
		{-1, 0, 3, 0},
		{3, 60, 3, 60},
		{10, 60, 10, 60},
	}
	for _, c := range cases {
		d, e := normalizeDupli(c.dupli, c.expire)
		if d != c.wantDupli || e != c.wantExpire {
			t.Errorf("normalizeDupli(%d,%d) = %d,%d want %d,%d", c.dupli, c.expire, d, e, c.wantDupli, c.wantExpire)
		}
	}
}

func TestGdAppParamsNormalizes(t *testing.T) {
	p := gdAppParams(&servicepb.CreateGdAppRequest{Name: "n", DupliTextCount: 1, TextCountExpire: -1, MarkStar1: true})
	if p.DupliTextCount != 3 || p.TextCountExpire != 0 || p.Name != "n" || !p.MarkStar1 {
		t.Fatalf("unexpected params %+v", p)
	}
}

func TestDefaultCopyTypes(t *testing.T) {
	if defaultCopyTypes != "255" {
		t.Fatalf("defaultCopyTypes = %q, want 255 (CHATROOM_MSG_TYPE_ALL)", defaultCopyTypes)
	}
}

func TestAppUpdatedRequestCarriesAllCopySettings(t *testing.T) {
	a := &store.AppInfo{
		AppId: "a", AppSecret: "s", MsgCpUrl: "m", TextCpUrl: "t", CustomCpUrl: "c",
		Types: "1,2", Qps: 1, TextQps: 2, CustomQps: 3,
	}
	r := appUpdatedRequest(a, "trace")
	if r.AppId != "a" || r.AppSecret != "s" || r.MsgCpUrl != "m" || r.TextCpUrl != "t" ||
		r.CustomCpUrl != "c" || r.Types != "1,2" || r.Qps != 1 || r.TextQps != 2 ||
		r.CustomQps != 3 || r.MsgTraceId != "trace" {
		t.Fatalf("unexpected request %v", r)
	}
}

func TestPcAppReply(t *testing.T) {
	ret, info := pcAppReply(nil)
	if ret.ErrorCode != servicepb.ErrorCode_CORE_APP_ID_NOT_EXISTS || info != nil {
		t.Fatal("nil app must map to CORE_APP_ID_NOT_EXISTS")
	}
	ret, info = pcAppReply(&store.PcAppInfo{Id: 1, AppId: "p", GdAppId: "g", ErrPass: true})
	if ret.ErrorCode != servicepb.ErrorCode_SUCCESS || info.AppId != "p" || info.GdAppId != "g" || !info.ErrPass {
		t.Fatalf("unexpected reply %v %v", ret, info)
	}
}
