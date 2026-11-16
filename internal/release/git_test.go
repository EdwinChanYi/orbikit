package release

import (
	"context"
	"strings"
	"testing"
)

func TestFetchUsesTagsAndHidesTheToken(t *testing.T) {
	var cmds [][]string
	source := GitSource{
		Username: "git",
		Token:    "secret-token",
		run: func(_ context.Context, _ string, args ...string) (string, error) {
			cmds = append(cmds, append([]string(nil), args...))
			return "remote secret-token\n", nil
		},
	}
	logText, err := source.Fetch(context.Background(), "checkout", RefTag, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logText, "secret-token") {
		t.Fatalf("log leaked the token: %s", logText)
	}
	joined := strings.Join(flatten(cmds), " ")
	if !strings.Contains(joined, "refs/tags/v1.2.3:refs/tags/v1.2.3") || strings.Contains(joined, "refs/heads") {
		t.Fatalf("commands = %s", joined)
	}
}

func TestFetchSHADoesNotUseABranch(t *testing.T) {
	var cmds [][]string
	sha := "0123456789abcdef0123456789abcdef01234567"
	source := GitSource{run: func(_ context.Context, _ string, args ...string) (string, error) {
		cmds = append(cmds, append([]string(nil), args...))
		return "", nil
	}}
	if _, err := source.Fetch(context.Background(), "checkout", RefSHA, sha); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(flatten(cmds), " ")
	if !strings.Contains(joined, sha) || strings.Contains(joined, "refs/heads") || strings.Contains(joined, "refs/tags") {
		t.Fatalf("commands = %s", joined)
	}
}

func flatten(cmds [][]string) []string {
	var out []string
	for _, cmd := range cmds {
		out = append(out, cmd...)
	}
	return out
}
