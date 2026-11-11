package store

import "testing"

func TestAppUpdateColumnsQPS(t *testing.T) {
	cols := appUpdateColumns(UpdateAppParams{QPS: 50, TextQPS: 0, CustomQPS: 20})
	if cols["qps"] != int64(50) || cols["text_qps"] != int64(50) || cols["custom_qps"] != int64(20) {
		t.Fatalf("qps columns = %v/%v/%v", cols["qps"], cols["text_qps"], cols["custom_qps"])
	}

	cols = appUpdateColumns(UpdateAppParams{QPS: 0, TextQPS: 10, CustomQPS: 10})
	for _, k := range []string{"qps", "text_qps", "custom_qps"} {
		if _, ok := cols[k]; ok {
			t.Fatalf("%s must not change when qps <= 0", k)
		}
	}
}

func TestAppUpdateColumnsOptionalFields(t *testing.T) {
	cols := appUpdateColumns(UpdateAppParams{Name: "", Desc: ""})
	if _, ok := cols["types"]; ok {
		t.Fatal("empty types must not be written")
	}
	if _, ok := cols["app_secret"]; ok {
		t.Fatal("secret must not change unless requested")
	}
	if v, ok := cols["name"]; !ok || v != "" {
		t.Fatal("name is always overwritten")
	}

	cols = appUpdateColumns(UpdateAppParams{Types: "1,2", RefreshSecret: true})
	if cols["types"] != "1,2" {
		t.Fatalf("types = %v", cols["types"])
	}
	if s, _ := cols["app_secret"].(string); len(s) != 64 {
		t.Fatalf("refreshed secret length = %d", len(s))
	}
}

func TestGdAppUpdateColumns(t *testing.T) {
	cols := gdAppUpdateColumns(GdAppParams{MarkStar: false, MsgLenMax: 0})
	for _, k := range []string{"name", "desc", "types"} {
		if _, ok := cols[k]; ok {
			t.Fatalf("empty %s must not be written", k)
		}
	}
	if v, ok := cols["mark_star"]; !ok || v != false {
		t.Fatal("bool flags are always overwritten")
	}
	if v, ok := cols["msg_len_max"]; !ok || v != int32(0) {
		t.Fatal("numeric limits are always overwritten")
	}

	cols = gdAppUpdateColumns(GdAppParams{Name: "n", Desc: "d", Types: "1"})
	if cols["name"] != "n" || cols["desc"] != "d" || cols["types"] != "1" {
		t.Fatalf("unexpected cols %v", cols)
	}
}
