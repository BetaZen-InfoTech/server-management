package services

import (
	"errors"
	"strings"
	"testing"
)

// The real git plumbing error a private-repo import surfaces when no PAT is
// supplied (from a live incident — see gitCloneHint).
const privateCloneErr = `command failed: exit status 128: Cloning into '/home/easycrm4u/projects/bizenly-platform-dev.src'...
warning: redirecting to https://github.com/BetaZen-InfoTech/bizenly.git/
fatal: could not read Username for 'https://github.com': No such device or address
fatal: expected flush after ref listing`

func TestGitCloneHint_PrivateRepoNoToken(t *testing.T) {
	h := gitCloneHint(privateCloneErr, false)
	if h == "" {
		t.Fatal("expected a hint for a 'could not read Username' failure, got none")
	}
	if !strings.Contains(h, "no Personal Access Token was supplied") {
		t.Errorf("no-token hint should say a PAT was missing; got %q", h)
	}
}

func TestGitCloneHint_BadToken(t *testing.T) {
	h := gitCloneHint(privateCloneErr, true)
	if !strings.Contains(strings.ToLower(h), "invalid") {
		t.Errorf("had-token hint should flag the PAT as invalid/expired; got %q", h)
	}
}

// GitHub's current wording for a bad/expired/revoked PAT over HTTPS — the exact
// message a project redeploy surfaced in the field (v3.1.285). The older
// "invalid username or password" matcher missed it, so no hint was shown.
const badTokenFetchErr = `git fetch: command failed: exit status 128: remote: Invalid username or token. Password authentication is not supported for Git operations.
fatal: Authentication failed for 'https://github.com/BetaZen-InfoTech/bizenly.git/'`

func TestGitCloneHint_InvalidToken_ModernGitHubWording(t *testing.T) {
	h := gitCloneHint(badTokenFetchErr, true)
	if h == "" {
		t.Fatal("expected a hint for GitHub's 'Invalid username or token' failure, got none")
	}
	if !strings.Contains(strings.ToLower(h), "invalid") || !strings.Contains(h, "fresh PAT") {
		t.Errorf("had-token hint should flag the PAT as invalid and tell the operator to paste a fresh one; got %q", h)
	}
}

func TestGitCloneHint_RepoNotFound(t *testing.T) {
	if h := gitCloneHint("fatal: repository not found", true); h == "" ||
		!strings.Contains(strings.ToLower(h), "not found") {
		t.Errorf("expected a repo-not-found hint; got %q", h)
	}
}

func TestGitCloneHint_UnknownErrorNoHint(t *testing.T) {
	if h := gitCloneHint("fatal: unable to access: Could not resolve host: github.com", false); h != "" {
		t.Errorf("unrecognised errors must not get a hint; got %q", h)
	}
}

func TestSanitiseGitError_RedactsTokenAndAppendsHint(t *testing.T) {
	tok := "ghp_SUPERSECRETTOKEN1234567890"
	err := errors.New("exit status 128: could not read Username for 'https://" + tok + "@github.com'")
	out := sanitiseGitError(err, tok)
	if strings.Contains(out, tok) {
		t.Fatalf("token must be redacted; got %q", out)
	}
	if !strings.Contains(out, "***") {
		t.Errorf("redaction marker missing; got %q", out)
	}
	if !strings.Contains(out, "GitHub authentication failed") {
		t.Errorf("expected the auth hint to be appended; got %q", out)
	}
}
