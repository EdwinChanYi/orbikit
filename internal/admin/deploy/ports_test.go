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

func TestNacosStaysOffTheHost(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	services := mappingChild(doc.Content[0], "services")
	nacos := mappingChild(services, "nacos")
	if nacos == nil {
		t.Fatal("nacos service missing")
	}
	if got := scalar(nacos, "image"); got != "nacos/nacos-server:v2.5.5" {
		t.Fatalf("image = %q", got)
	}
	profiles := sequence(nacos, "profiles")
	if len(profiles) != 1 || profiles[0] != "nacos" {
		t.Fatalf("profiles = %v", profiles)
	}
	if mappingChild(nacos, "ports") != nil {
		t.Fatal("nacos publishes a host port")
	}
	core := mappingChild(services, "core")
	depends := mappingChild(core, "depends_on")
	if mappingChild(depends, "nacos") != nil {
		t.Fatal("core depends on nacos")
	}
}

func mappingChild(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node, key string) string {
	child := mappingChild(n, key)
	if child == nil {
		return ""
	}
	return child.Value
}

func sequence(n *yaml.Node, key string) []string {
	child := mappingChild(n, key)
	if child == nil || child.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, len(child.Content))
	for i, item := range child.Content {
		out[i] = item.Value
	}
	return out
}

func TestBuilderAloneMountsDockerSocket(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Ports   []string `yaml:"ports"`
			User    string   `yaml:"user"`
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	builder := doc.Services["builder"]
	if len(builder.Ports) != 0 {
		t.Fatalf("builder ports = %v", builder.Ports)
	}
	if builder.User != "root" {
		t.Fatalf("builder user = %q", builder.User)
	}
	if len(builder.Volumes) != 2 || builder.Volumes[0] != "./configs:/app/configs:ro" || builder.Volumes[1] != "/var/run/docker.sock:/var/run/docker.sock" {
		t.Fatalf("builder volumes = %v", builder.Volumes)
	}
	for _, volume := range doc.Services["admin"].Volumes {
		if volume == "/var/run/docker.sock:/var/run/docker.sock" {
			t.Fatal("admin mounts the docker socket")
		}
	}
}
