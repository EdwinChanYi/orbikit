package nacos

import "testing"

func TestClassifySplitsHotAndRestart(t *testing.T) {
	before := []byte("log:\n  level: info\ncache_expire: 60s\ngrpc_addr: \":9001\"\nmysql:\n  dsn: old\n")
	after := []byte("log:\n  level: debug\ncache_expire: 15s\ngrpc_addr: \":9001\"\nmysql:\n  dsn: new\n")
	hot, restart, err := Classify("core", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(hot) != 2 || hot[0].Path != "log.level" || hot[0].Value != "debug" || hot[1].Path != "cache_expire" || hot[1].Value != "15s" {
		t.Fatalf("hot = %+v", hot)
	}
	if len(restart) != 1 || restart[0] != "mysql.dsn" {
		t.Fatalf("restart = %+v", restart)
	}
}

func TestClassifyTreatsModerationAsOneTree(t *testing.T) {
	before := []byte("log:\n  level: info\nmoderation:\n  type: none\n")
	after := []byte("log:\n  level: info\nmoderation:\n  type: keyword\n")
	hot, restart, err := Classify("gateway", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(hot) != 1 || hot[0].Path != "moderation" || len(restart) != 0 {
		t.Fatalf("hot = %+v, restart = %+v", hot, restart)
	}
}

func TestClassifyChatroomOnlyHotReloadsLogLevel(t *testing.T) {
	before := []byte("log:\n  level: info\nenter_token_ttl: 1m\n")
	after := []byte("log:\n  level: debug\nenter_token_ttl: 2m\n")
	hot, restart, err := Classify("chatroom", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(hot) != 1 || hot[0].Path != "log.level" || hot[0].Value != "debug" {
		t.Fatalf("hot = %+v", hot)
	}
	if len(restart) != 1 || restart[0] != "enter_token_ttl" {
		t.Fatalf("restart = %+v", restart)
	}
}

func TestClassifyBuilderOnlyHotReloadsLogLevel(t *testing.T) {
	before := []byte("log:\n  level: info\nhttp_addr: \":8092\"\n")
	after := []byte("log:\n  level: debug\nhttp_addr: \":8093\"\n")
	hot, restart, err := Classify("builder", before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(hot) != 1 || hot[0].Path != "log.level" || hot[0].Value != "debug" {
		t.Fatalf("hot = %+v", hot)
	}
	if len(restart) != 1 || restart[0] != "http_addr" {
		t.Fatalf("restart = %+v", restart)
	}
}
