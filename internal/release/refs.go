package release

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RemoteRef is one tag or commit the operator can build.
type RemoteRef struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	Kind        string `json:"kind"`
	Summary     string `json:"summary"`
	CommittedAt string `json:"committed_at"`
}

// ListRemote reads tags and recent commits from the fixed GitHub repository.
// The token is sent as a header and is never included in an error.
func ListRemote(ctx context.Context, username, token string, client *http.Client, base string) ([]RemoteRef, error) {
	if username == "" || token == "" {
		return nil, errors.New("缺少 Git 凭证")
	}
	repo, err := githubRepo()
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if base == "" {
		base = "https://api.github.com"
	}
	var tags []ghTag
	if err := githubGet(ctx, client, base+"/repos/"+repo+"/tags?per_page=100", token, &tags); err != nil {
		return nil, err
	}
	var commits []ghCommit
	if err := githubGet(ctx, client, base+"/repos/"+repo+"/commits?per_page=30", token, &commits); err != nil {
		return nil, err
	}
	out := make([]RemoteRef, 0, len(tags)+len(commits))
	for _, tag := range tags {
		if _, err := ParseRef(tag.Name); err != nil || shaRef.MatchString(tag.Name) {
			continue
		}
		out = append(out, RemoteRef{Name: tag.Name, Ref: tag.Name, Kind: RefTag})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return newerTag(out[i].Name, out[j].Name)
	})
	for _, commit := range commits {
		if !shaRef.MatchString(commit.SHA) {
			continue
		}
		out = append(out, RemoteRef{
			Name:        commit.SHA[:12],
			Ref:         commit.SHA,
			Kind:        "commit",
			Summary:     firstLine(commit.Commit.Message),
			CommittedAt: commit.Commit.Author.Date,
		})
	}
	return out, nil
}

func githubRepo() (string, error) {
	parsed, err := url.Parse(RepoURL)
	if err != nil {
		return "", errors.New("拉取失败")
	}
	path := strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
	if strings.Count(path, "/") != 1 || strings.Contains(path, "..") {
		return "", errors.New("拉取失败")
	}
	return path, nil
}

func githubGet(ctx context.Context, client *http.Client, endpoint, token string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("拉取失败")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "orbikit-builder")
	res, err := client.Do(req)
	if err != nil {
		return errors.New("拉取失败")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return errors.New("拉取失败")
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return errors.New("缺少 Git 凭证")
	}
	if res.StatusCode != http.StatusOK {
		return errors.New("拉取失败")
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return errors.New("拉取失败")
	}
	return nil
}

func newerTag(a, b string) bool {
	ap, bp := numbers(a), numbers(b)
	if len(ap) == 0 || len(bp) == 0 {
		return a > b
	}
	n := len(ap)
	if len(bp) < n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		if ap[i] != bp[i] {
			return ap[i] > bp[i]
		}
	}
	return len(ap) > len(bp)
}

func numbers(tag string) []int {
	var parts []int
	current := ""
	flush := func() {
		if current == "" {
			return
		}
		n, err := strconv.Atoi(current)
		if err == nil {
			parts = append(parts, n)
		}
		current = ""
	}
	for _, r := range tag {
		if r >= '0' && r <= '9' {
			current += string(r)
			continue
		}
		flush()
	}
	flush()
	return parts
}

func firstLine(message string) string {
	message = strings.TrimSpace(message)
	if i := strings.IndexAny(message, "\r\n"); i >= 0 {
		message = strings.TrimSpace(message[:i])
	}
	return message
}

type ghTag struct {
	Name string `json:"name"`
}

type ghCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}
