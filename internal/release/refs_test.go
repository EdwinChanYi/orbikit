package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListRemoteSortsTagsAndKeepsCommits(t *testing.T) {
	const token = "secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("auth = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/repos/EdwinChanYi/orbikit/tags":
			_, _ = w.Write([]byte(`[
				{"name":"v0.0.2"},
				{"name":"feature/login"},
				{"name":"v0.0.10"}
			]`))
		case "/repos/EdwinChanYi/orbikit/commits":
			_, _ = w.Write([]byte(`[
				{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","commit":{"message":"fix login\n\nbody","author":{"date":"2026-11-15T14:52:00Z"}}}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	refs, err := ListRemote(context.Background(), "git", token, srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 || refs[0].Ref != "v0.0.10" || refs[1].Ref != "v0.0.2" || refs[2].Kind != "commit" {
		t.Fatalf("refs = %+v", refs)
	}
	if refs[2].Ref != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || refs[2].Summary != "fix login" {
		t.Fatalf("commit = %+v", refs[2])
	}
}

func TestListRemoteRejectsMissingCredentialsWithoutCallingGitHub(t *testing.T) {
	refs, err := ListRemote(context.Background(), "", "secret-token", nil, "http://127.0.0.1:1")
	if err == nil || err.Error() != "缺少 Git 凭证" || refs != nil {
		t.Fatalf("refs = %+v err = %v", refs, err)
	}
}

func TestListRemoteHidesTheTokenWhenGitHubRejectsIt(t *testing.T) {
	const token = "secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad "+token, http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := ListRemote(context.Background(), "git", token, srv.Client(), srv.URL)
	if err == nil || err.Error() != "缺少 Git 凭证" || strings.Contains(err.Error(), token) {
		t.Fatalf("err = %v", err)
	}
}
