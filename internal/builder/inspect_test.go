package builder

import "testing"

func TestAllowedNames(t *testing.T) {
	for _, name := range []string{"core", "chatroom", "gateway", "webhook"} {
		if !Allowed(name) {
			t.Fatalf("%s should be allowed", name)
		}
	}
	for _, name := range []string{"", "admin", "core;touch", "../core", "Core"} {
		if Allowed(name) {
			t.Fatalf("%q should be rejected", name)
		}
	}
}

func TestContainerNameUsesWhitelist(t *testing.T) {
	got, err := ContainerName("orbikit", "core")
	if err != nil || got != "orbikit-core-1" {
		t.Fatalf("name = %q, err = %v", got, err)
	}
	if _, err := ContainerName("orbikit", "admin"); err == nil {
		t.Fatal("admin was accepted")
	}
	if _, err := ContainerName("Orbikit", "core"); err == nil {
		t.Fatal("project with uppercase was accepted")
	}
}
