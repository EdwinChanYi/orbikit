package release

import "testing"

func TestParseRefAcceptsTagAndFullSHA(t *testing.T) {
	kind, err := ParseRef("v1.2.3")
	if err != nil || kind != RefTag {
		t.Fatalf("tag = %s %v", kind, err)
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	kind, err = ParseRef(sha)
	if err != nil || kind != RefSHA {
		t.Fatalf("sha = %s %v", kind, err)
	}
}

func TestParseRefRejectsShortSHAAndBranches(t *testing.T) {
	for _, ref := range []string{"", "abc1234", "feature/login", "-v1", "main/topic"} {
		if _, err := ParseRef(ref); err == nil {
			t.Fatalf("%q was accepted", ref)
		}
	}
	if _, err := ParseRef("main"); err != nil {
		t.Fatalf("main can be a tag name: %v", err)
	}
}

func TestReleaseNames(t *testing.T) {
	for _, name := range []string{"core", "chatroom", "gateway", "webhook", "admin", "builder"} {
		if !Allowed(name) {
			t.Fatalf("%s should be allowed", name)
		}
	}
	for _, name := range []string{"", "core;touch", "../core", "Core"} {
		if Allowed(name) {
			t.Fatalf("%s should be rejected", name)
		}
		if _, err := ContainerName("orbikit", name); err == nil {
			t.Fatalf("%s produced a container name", name)
		}
	}
	got, err := ContainerName("orbikit", "admin")
	if err != nil || got != "orbikit-admin-1" {
		t.Fatalf("container = %s %v", got, err)
	}
}

func TestTailKeepsTheLast256KB(t *testing.T) {
	text := make([]byte, LogLimit+3)
	for i := range text {
		text[i] = 'a'
	}
	copy(text[len(text)-3:], "xyz")
	got := Tail(string(text))
	if len(got) != LogLimit || got[len(got)-3:] != "xyz" {
		t.Fatalf("tail len = %d suffix = %q", len(got), got[len(got)-3:])
	}
}
