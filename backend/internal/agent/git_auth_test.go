package agent

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGitRepoHost(t *testing.T) {
	cases := map[string]string{
		"https://github.com/owner/repo.git":                  "github.com",
		"https://github.com/owner/repo":                      "github.com",
		"https://ghp_tok@github.com/owner/repo.git":           "github.com",
		"https://x-access-token:ghp_tok@github.com/o/r.git":   "github.com",
		"https://gitlab.example.com:443/o/r.git":              "gitlab.example.com:443",
		"git@github.com:owner/repo.git":                       "", // ssh — not this path
		"":                                                    "",
	}
	for in, want := range cases {
		if got := GitRepoHost(in); got != want {
			t.Errorf("GitRepoHost(%q)=%q want %q", in, got, want)
		}
	}
}

func TestStripURLToken(t *testing.T) {
	cases := map[string]string{
		"https://ghp_tok@github.com/owner/repo.git":         "https://github.com/owner/repo.git",
		"https://x-access-token:ghp_tok@github.com/o/r.git": "https://github.com/o/r.git",
		"https://github.com/owner/repo.git":                 "https://github.com/owner/repo.git", // unchanged
		"https://github.com/owner/repo@2x.git":              "https://github.com/owner/repo@2x.git", // @ in path, not authority
	}
	for in, want := range cases {
		if got := StripURLToken(in); got != want {
			t.Errorf("StripURLToken(%q)=%q want %q", in, got, want)
		}
	}
}

func TestGitAuthArgs(t *testing.T) {
	// No token, or non-https -> no args (public repo / ssh).
	if GitAuthArgs("https://github.com/o/r.git", "") != nil {
		t.Error("empty token must yield nil auth args")
	}
	if GitAuthArgs("git@github.com:o/r.git", "ghp_x") != nil {
		t.Error("ssh URL must yield nil auth args")
	}

	// With a token: clear inherited helper, then a host-scoped credential
	// helper that feeds the (base64'd) PAT. The token must NOT appear in
	// cleartext, and its base64 must decode back to the token.
	args := GitAuthArgs("https://github.com/o/r.git", "ghp_SECRET")
	if len(args) != 4 || args[0] != "-c" || args[1] != "credential.helper=" || args[2] != "-c" {
		t.Fatalf("expected [-c credential.helper= -c <scoped helper>], got %v", args)
	}
	cfg := args[3]
	if !strings.HasPrefix(cfg, "credential.https://github.com.helper=") {
		t.Fatalf("helper must be scoped to the host, got %q", cfg)
	}
	if strings.Contains(cfg, "ghp_SECRET") {
		t.Errorf("token leaked in cleartext: %q", cfg)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte("ghp_SECRET"))
	if !strings.Contains(cfg, b64) {
		t.Errorf("helper should carry the base64'd token %q; got %q", b64, cfg)
	}
}
