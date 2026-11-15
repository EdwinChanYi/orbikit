package admin

import (
	"strings"
	"testing"

	"github.com/EdwinChanYi/orbikit/pkg/config"
)

func TestDefaultListenAddr(t *testing.T) {
	if DefaultConfig().HTTPAddr != ":8091" {
		t.Fatalf("addr = %s", DefaultConfig().HTTPAddr)
	}
}

func TestExampleConfig(t *testing.T) {
	t.Setenv("MYSQL_PASSWORD", "secret")
	var cfg Config
	if err := config.Load("../../configs/admin.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8091" {
		t.Fatalf("addr = %s", cfg.HTTPAddr)
	}
	if cfg.MySQLDSN == "" || strings.Contains(cfg.MySQLDSN, "${") {
		t.Fatalf("dsn = %s", cfg.MySQLDSN)
	}
	if !cfg.Nacos.Enabled || cfg.Nacos.Addr != "http://nacos:8848" {
		t.Fatalf("nacos = %+v", cfg.Nacos)
	}
}
