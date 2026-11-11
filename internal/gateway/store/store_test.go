package store

import "testing"

func TestNewConnectionEventRoundTrip(t *testing.T) {
	payload := NewConnectionEvent(12, 345, "app-1", 6789)
	if payload != "MARS_AGENT:NEW_CONNECTION_EVENT:12:345:app-1:6789" {
		t.Fatalf("payload = %q", payload)
	}
	m, c, app, uid, err := ParseNewConnectionEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	if m != 12 || c != 345 || app != "app-1" || uid != 6789 {
		t.Fatalf("parsed %d %d %q %d", m, c, app, uid)
	}
}

func TestParseNewConnectionEventRejectsMalformed(t *testing.T) {
	for _, p := range []string{
		"",
		"OTHER:1:2:app:3",
		"MARS_AGENT:NEW_CONNECTION_EVENT:1:2:app",
		"MARS_AGENT:NEW_CONNECTION_EVENT:x:2:app:3",
		"MARS_AGENT:NEW_CONNECTION_EVENT:70000:2:app:3",
		"MARS_AGENT:NEW_CONNECTION_EVENT:1:2:a:b:3",
	} {
		if _, _, _, _, err := ParseNewConnectionEvent(p); err == nil {
			t.Errorf("ParseNewConnectionEvent(%q) succeeded", p)
		}
	}
}
