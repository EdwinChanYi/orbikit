package release

import (
	"context"
	"net/url"
	"os/exec"
	"strings"
)

// GitSource fetches the fixed repository with a username and token.
type GitSource struct {
	Repo     string
	Username string
	Token    string
	run      func(ctx context.Context, name string, args ...string) (string, error)
}

// Fetch checks out one tag or one full SHA. It does not fetch branches.
func (g GitSource) Fetch(ctx context.Context, dir, kind, ref string) (string, error) {
	run := g.run
	if run == nil {
		run = execCombined
	}
	remote := g.remote()
	var steps [][]string
	steps = append(steps,
		[]string{"init", dir},
		[]string{"-C", dir, "remote", "add", "origin", remote},
	)
	if kind == RefSHA {
		steps = append(steps,
			[]string{"-C", dir, "fetch", "--depth", "1", "origin", ref},
			[]string{"-C", dir, "checkout", "FETCH_HEAD"},
		)
	} else {
		refspec := "refs/tags/" + ref + ":refs/tags/" + ref
		steps = append(steps,
			[]string{"-C", dir, "fetch", "--depth", "1", "origin", refspec},
			[]string{"-C", dir, "checkout", "refs/tags/" + ref},
		)
	}
	var logText string
	for _, args := range steps {
		out, err := run(ctx, "git", args...)
		logText += redact(out, g.Token)
		if err != nil {
			return logText, err
		}
	}
	return logText, nil
}

func (g GitSource) remote() string {
	repo := g.Repo
	if repo == "" {
		repo = RepoURL
	}
	parsed, err := url.Parse(repo)
	if err != nil || g.Username == "" || g.Token == "" {
		return repo
	}
	parsed.User = url.UserPassword(g.Username, g.Token)
	return parsed.String()
}

func execCombined(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func redact(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "***")
}
