package deploy

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestComposeDoesNotPublishInternalPorts(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if ports := doc.Services["core"].Ports; len(ports) != 0 {
		t.Fatalf("core ports = %v", ports)
	}
	if ports := doc.Services["chatroom"].Ports; len(ports) != 0 {
		t.Fatalf("chatroom ports = %v", ports)
	}
	got := doc.Services["gateway"].Ports
	want := []string{"8000:8000", "8001:8001"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("gateway ports = %v, want %v", got, want)
	}
}
