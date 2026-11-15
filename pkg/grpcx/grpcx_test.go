package grpcx

import "testing"

func TestDialDiscoveredRejectsEtcdWithoutRegistry(t *testing.T) {
	if _, err := DialDiscovered("etcd:///core", nil); err == nil {
		t.Fatal("expected an error")
	}
	conn, err := DialDiscovered("127.0.0.1:1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}
