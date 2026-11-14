package nacos

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FieldChange is one hot path and the value from the new document.
type FieldChange struct {
	Path  string
	Value string
}

// HotPaths lists the paths a service may apply without a restart.
func HotPaths(service string) []string {
	switch service {
	case "core":
		return []string{"log.level", "cache_expire"}
	case "chatroom":
		return []string{"log.level"}
	case "webhook":
		return []string{"log.level", "cache.expire", "cache.not_found_expire"}
	case "gateway":
		return []string{"log.level", "moderation"}
	default:
		return []string{}
	}
}

// Classify splits changed paths into hot updates and paths that need a restart.
func Classify(service string, before, after []byte) ([]FieldChange, []string, error) {
	oldDoc, err := mapping(before)
	if err != nil {
		return nil, nil, err
	}
	newDoc, err := mapping(after)
	if err != nil {
		return nil, nil, err
	}
	oldFlat := flatten(oldDoc)
	newFlat := flatten(newDoc)
	paths := map[string]struct{}{}
	for path := range oldFlat {
		paths[path] = struct{}{}
	}
	for path := range newFlat {
		paths[path] = struct{}{}
	}
	hotSet := map[string]bool{}
	for _, path := range HotPaths(service) {
		hotSet[path] = true
	}
	var hot []FieldChange
	for _, path := range HotPaths(service) {
		if _, ok := paths[path]; !ok {
			continue
		}
		if oldFlat[path] == newFlat[path] {
			continue
		}
		hot = append(hot, FieldChange{Path: path, Value: newFlat[path]})
	}
	var restart []string
	for path := range paths {
		if hotSet[path] || oldFlat[path] == newFlat[path] {
			continue
		}
		restart = append(restart, path)
	}
	sort.Strings(restart)
	return hot, restart, nil
}

func mapping(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config is not a mapping")
	}
	return doc.Content[0], nil
}

func flatten(node *yaml.Node) map[string]string {
	out := map[string]string{}
	walk(node, "", out)
	return out
}

func walk(node *yaml.Node, prefix string, out map[string]string) {
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if val.Kind == yaml.MappingNode && path != "moderation" {
			walk(val, path, out)
			continue
		}
		if val.Kind == yaml.ScalarNode {
			out[path] = val.Value
			continue
		}
		encoded, err := yaml.Marshal(val)
		if err != nil {
			out[path] = ""
			continue
		}
		out[path] = strings.TrimSpace(string(bytes.TrimSpace(encoded)))
	}
}
