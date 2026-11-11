package watcher

import "testing"

func TestMergeNotifyFlags(t *testing.T) {
	tests := []struct {
		cur, want string
		changed   bool
	}{
		{cur: "", want: "Ex", changed: true},
		{cur: "Ex", want: "Ex", changed: false},
		{cur: "xE", want: "xE", changed: false},
		{cur: "Kx", want: "KxE", changed: true},
		{cur: "E", want: "Ex", changed: true},
		{cur: "AE", want: "AE", changed: false},
		{cur: "KA", want: "KAE", changed: true},
		{cur: "Elg", want: "Elgx", changed: true},
	}
	for _, tt := range tests {
		got, changed := mergeNotifyFlags(tt.cur)
		if got != tt.want || changed != tt.changed {
			t.Errorf("mergeNotifyFlags(%q) = %q, %v; want %q, %v", tt.cur, got, changed, tt.want, tt.changed)
		}
	}
}

func TestExpiredChannel(t *testing.T) {
	if got := expiredChannel(3); got != "__keyevent@3__:expired" {
		t.Fatalf("expiredChannel(3) = %q", got)
	}
}
