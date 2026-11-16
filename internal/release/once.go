package release

import (
	"context"
	"fmt"
	"os"
	"time"
)

// RunOnce executes one task inside the one-shot container.
// ADMIN_DSN, the Git credentials, and ORBIKIT_PROJECT come from the environment.
func RunOnce(id uint64) error {
	ctx := context.Background()
	store, err := OpenMySQL(ctx, os.Getenv("ADMIN_DSN"))
	if err != nil {
		return err
	}
	project := os.Getenv("ORBIKIT_PROJECT")
	if project == "" {
		project = "orbikit"
	}
	proxy := os.Getenv("GOPROXY")
	if proxy == "" {
		proxy = "https://proxy.golang.org,direct"
	}
	return Run(ctx, Deps{
		Store:   store,
		Source:  GitSource{Username: os.Getenv("ORBIKIT_GIT_USERNAME"), Token: os.Getenv("ORBIKIT_GIT_TOKEN")},
		Engine:  &Docker{Socket: envOr("DOCKER_SOCKET", "/var/run/docker.sock")},
		Project: project,
		GoProxy: proxy,
		Now:     time.Now,
	}, id)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// FormatRunError is the process exit text for a one-shot failure.
func FormatRunError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("release: %v", err)
}
