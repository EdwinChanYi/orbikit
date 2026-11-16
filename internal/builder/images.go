package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ImageVersion is one locally built image tag for a service.
type ImageVersion struct {
	Tag       string    `json:"tag"`
	ImageID   string    `json:"image_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Images lists tags of orbikit-<service> that exist on this Docker engine.
// Newest images come first. An empty list is not an error.
func (d *Docker) Images(ctx context.Context, service string) ([]ImageVersion, error) {
	if d == nil {
		return nil, fmt.Errorf("docker socket is not configured")
	}
	code, raw, err := d.call(ctx, http.MethodGet, "/images/json", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("docker images: %d", code)
	}
	var rows []struct {
		ID       string   `json:"Id"`
		RepoTags []string `json:"RepoTags"`
		Created  int64    `json:"Created"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	prefix := "orbikit-" + service + ":"
	out := []ImageVersion{}
	for _, row := range rows {
		for _, repo := range row.RepoTags {
			tag, ok := strings.CutPrefix(repo, prefix)
			if !ok || tag == "" || strings.Contains(tag, ":") || tag == "<none>" {
				continue
			}
			out = append(out, ImageVersion{
				Tag:       tag,
				ImageID:   row.ID,
				CreatedAt: time.Unix(row.Created, 0).UTC(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].Tag < out[j].Tag
	})
	return out, nil
}
