package store

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func parse(t *testing.T, model any) *schema.Schema {
	t.Helper()
	s, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse %T: %v", model, err)
	}
	return s
}

func TestModelSchemas(t *testing.T) {
	cases := []struct {
		model   any
		table   string
		columns []string
	}{
		{&AppInfo{}, "app_infos", []string{
			"id", "app_id", "app_secret", "name", "msg_cp_url", "text_cp_url", "custom_cp_url", "desc",
			"types", "qps", "text_qps", "custom_qps", "media_app_id", "media_app_secret", "gd_app_id",
			"created_at", "updated_at",
		}},
		{&GdAppInfo{}, "gd_app_infos", []string{
			"id", "app_id", "app_secret", "name", "msg_cp_url", "text_cp_url", "custom_cp_url", "desc",
			"types", "qps", "text_qps", "custom_qps", "match_level", "no_strict_types", "need_match_base",
			"mark_star", "mark_star1", "star_types1", "mark_star2", "star_types2", "msg_len_max",
			"msg_digital_count_max", "bussiness_need_check_yidun_url", "media_app_id", "gd_app_id",
			"text_count_expire", "dupli_text_count", "is_black_dupli_user", "is_add_word_store",
			"created_at", "updated_at",
		}},
		{&PcAppInfo{}, "pc_app_infos", []string{
			"id", "app_id", "app_secret", "name", "desc", "gd_app_id", "business_url", "err_pass",
			"created_at", "updated_at",
		}},
		{&RestrainUser{}, "restrain_users", []string{"id", "app_id", "uid", "is_restrain", "create_at"}},
	}
	for _, c := range cases {
		s := parse(t, c.model)
		if s.Table != c.table {
			t.Errorf("%T table = %s, want %s", c.model, s.Table, c.table)
		}
		if len(s.DBNames) != len(c.columns) {
			t.Errorf("%T columns = %v, want %v", c.model, s.DBNames, c.columns)
			continue
		}
		for i, col := range c.columns {
			if s.DBNames[i] != col {
				t.Errorf("%T column %d = %s, want %s", c.model, i, s.DBNames[i], col)
			}
		}
	}
}

func TestModelIndexes(t *testing.T) {
	idx := parse(t, &AppInfo{}).ParseIndexes()
	if i, ok := idx["uix_app_infos_app_id"]; !ok || i.Class != "UNIQUE" {
		t.Fatalf("app_infos unique index missing: %v", idx)
	}
	idx = parse(t, &RestrainUser{}).ParseIndexes()
	i, ok := idx["appid_uid_uidx"]
	if !ok || i.Class != "UNIQUE" || len(i.Fields) != 2 {
		t.Fatalf("restrain_users unique index missing: %v", idx)
	}
}

func TestModelDefaults(t *testing.T) {
	s := parse(t, &GdAppInfo{})
	if f := s.LookUpField("need_match_base"); f.DefaultValueInterface != true {
		t.Errorf("need_match_base default = %v", f.DefaultValueInterface)
	}
	if f := s.LookUpField("msg_digital_count_max"); f.DefaultValue != "6" {
		t.Errorf("msg_digital_count_max default = %q", f.DefaultValue)
	}
	if f := parse(t, &AppInfo{}).LookUpField("qps"); f.DefaultValue != "1000" {
		t.Errorf("qps default = %q", f.DefaultValue)
	}
}
