// Package config loads YAML configuration files.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads the YAML file at path into out. ${VAR} references are expanded
// from the environment before parsing, so secrets can stay out of the file.
func Load(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	if err := yaml.Unmarshal([]byte(expanded), out); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}
