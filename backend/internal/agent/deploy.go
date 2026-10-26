package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// GitRepoHost extracts the bare host (e.g. "github.com") from an https repo
// URL, tolerating an embedded "user@" / "user:pass@" credential prefix.
// Returns "" for non-https URLs (ssh remotes etc. don't use this auth path).
func GitRepoHost(repoURL string) string {
	u := strings.TrimSpace(repoURL)
	if !strings.HasPrefix(u, "https://") {
		return ""
	}
	u = u[len("https://"):]
	if slash := strings.Index(u, "/"); slash >= 0 {
		if at := strings.LastIndex(u[:slash], "@"); at >= 0 {
			u = u[at+1:]
		}
		u = u[:strings.Index(u, "/")]
	} else if at := strings.LastIndex(u, "@"); at >= 0 {
		u = u[at+1:]
	}
	return u
}

// StripURLToken removes an embedded "user@" / "user:pass@" credential from an
// https URL so a token is never persisted in .git/config or shown in a process
// arg. Only strips a credential inside the authority (before the first path
// slash); a bare "https://host/path" is returned unchanged.
func StripURLToken(repoURL string) string {
	u := strings.TrimSpace(repoURL)
	if !strings.HasPrefix(u, "https://") {
		return repoURL
	}
	rest := u[len("https://"):]
	slash := strings.Index(rest, "/")
	authority := rest
	if slash >= 0 {
		authority = rest[:slash]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return "https://" + rest
}

// GitAuthArgs returns `-c credential.helper=…` arguments that authenticate a
// git clone/fetch against a private repo with a PAT.
//
// Why a credential helper and not a URL-embedded token (the old way)? Git DROPS
// a URL-embedded credential when the server 301-redirects — GitHub does this
// (e.g. canonicalising the git-upload-pack request to a trailing slash), so the
// redirected request goes out unauthenticated and the clone dies with the
// cryptic "fatal: could not read Username for 'https://github.com'". An
// http.extraheader wasn't enough either: it's only sent proactively and does
// NOT answer git's auth *challenge* on a 401, so git still falls through to a
// username prompt. A scoped credential helper DOES answer the challenge — on
// every request, including the redirected one — so the PAT reliably reaches the
// server. (Verified: a dummy token now yields "Authentication failed" at the
// redirected URL instead of "could not read Username".)
//
// Details: the helper is scoped to the repo's host, clears any inherited helper
// first, and feeds "x-access-token:<pat>" (GitHub's canonical basic-auth form,
// also valid for GitLab/Bitbucket PATs-as-password). The token is base64'd and
// decoded inside the helper so a value containing shell metacharacters can't
// break out of the snippet. Empty token or non-https host => nil (public repo /
// ssh need no auth).
func GitAuthArgs(repoURL, token string) []string {
	token = strings.TrimSpace(token)
	host := GitRepoHost(repoURL)
	if token == "" || host == "" {
		return nil
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(token))
	helper := fmt.Sprintf(`!f() { printf 'username=x-access-token\npassword=%%s\n' "$(printf %%s %s | base64 -d)"; }; f`, b64)
	return []string{
		"-c", "credential.helper=", // drop any inherited/system helper first
		"-c", fmt.Sprintf("credential.https://%s.helper=%s", host, helper),
	}
}

func GitClone(ctx context.Context, repoURL, branch, destPath, token string) error {
	clean := StripURLToken(repoURL)
	args := GitAuthArgs(clean, token)
	args = append(args, "clone", "--depth", "1", "--branch", branch, clean, destPath)
	_, err := RunCommand(ctx, "git", args...)
	return err
}

func GitPull(ctx context.Context, repoPath, branch string) error {
	_, err := RunCommand(ctx, "git", "-C", repoPath, "pull", "origin", branch)
	return err
}

func CreateSystemdService(ctx context.Context, name, user, workDir, startCmd string, envVars map[string]string) error {
	envLines := ""
	for k, v := range envVars {
		envLines += fmt.Sprintf("Environment=%s=%s\n", k, v)
	}

	// systemd requires the first token of ExecStart to be an absolute path.
	// Wrap anything else (relative paths like ./venv/bin/..., shell builtins,
	// `bundle exec ...`, `npm start`, etc.) in a login bash so PATH / HOME
	// are set up and the workDir is respected.
	execStart := startCmd
	trimmed := strings.TrimSpace(startCmd)
	if !strings.HasPrefix(trimmed, "/") {
		// Escape single quotes inside the command before wrapping.
		escaped := strings.ReplaceAll(trimmed, "'", `'\''`)
		execStart = fmt.Sprintf("/bin/bash -lc 'cd %s && %s'", workDir, escaped)
	}

	unit := fmt.Sprintf(`[Unit]
Description=Betazen Server Panel App - %s
After=network-online.target
Wants=network-online.target
# Cap crash-restarts: 5 failures within 5 min -> the unit enters a visible
# failed state instead of looping forever behind a 502 (and burning CPU).
# Transient blips still get a few retries. (v3.1.122)
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
User=%s
Group=%s
WorkingDirectory=%s
ExecStart=%s
Restart=always
RestartSec=5
# fd/task ceilings: see CreateSystemdUnit (EMFILE freeze under high concurrency).
LimitNOFILE=65536:1048576
TasksMax=16384
%s
[Install]
WantedBy=multi-user.target
`, name, user, user, workDir, execStart, envLines)

	serviceName := fmt.Sprintf("sp-app-%s", name)
	path := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)
	if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
		return err
	}

	RunCommand(ctx, "systemctl", "daemon-reload")
	RunCommand(ctx, "systemctl", "enable", serviceName)
	// Use restart so that redeploys (new unit file contents, new port, new
	// ExecStart) actually pick up the new definition instead of silently
	// keeping the previously running process around.
	_, err := RunCommand(ctx, "systemctl", "restart", serviceName)
	return err
}

func DeleteSystemdService(ctx context.Context, name string) error {
	serviceName := fmt.Sprintf("sp-app-%s", name)
	RunCommand(ctx, "systemctl", "stop", serviceName)
	RunCommand(ctx, "systemctl", "disable", serviceName)
	os.Remove(fmt.Sprintf("/etc/systemd/system/%s.service", serviceName))
	_, err := RunCommand(ctx, "systemctl", "daemon-reload")
	return err
}

// CreateSystemdUnit is the name-explicit variant of CreateSystemdService used
// by the Deploy Software project service, which needs a naming scheme
// ("sp-proj-<slug>-<svc>") distinct from the single-app "sp-app-<name>"
// convention. The unit name must be a valid systemd identifier — the caller
// is responsible for the prefix; this function does no prefixing of its own.
func CreateSystemdUnit(ctx context.Context, unitName, user, workDir, startCmd string, envVars map[string]string) error {
	envLines := ""
	for k, v := range envVars {
		envLines += fmt.Sprintf("Environment=%s=%s\n", k, v)
	}

	execStart := startCmd
	trimmed := strings.TrimSpace(startCmd)
	if !strings.HasPrefix(trimmed, "/") {
		escaped := strings.ReplaceAll(trimmed, "'", `'\''`)
		execStart = fmt.Sprintf("/bin/bash -lc 'cd %s && %s'", workDir, escaped)
	}

	unit := fmt.Sprintf(`[Unit]
Description=Betazen Server Panel Project Service - %s
After=network-online.target
Wants=network-online.target
# Cap crash-restarts: 5 failures within 5 min -> the unit enters a visible
# failed state instead of looping forever behind a 502 (and burning CPU).
# Transient blips still get a few retries. (v3.1.122)
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
User=%s
Group=%s
WorkingDirectory=%s
ExecStart=%s
Restart=always
RestartSec=5
# Raise the fd/task ceilings: high-concurrency apps (e.g. a WhatsApp campaign
# bot) exhaust the default ~1024 fds -> EMFILE -> the API freezes under load.
# TasksMax=16384 avoids the default task cap for worker-thread / child-
# process heavy apps. Existing units get this via bzpanel heal-unit-limits.
LimitNOFILE=65536:1048576
TasksMax=16384
%s
[Install]
WantedBy=multi-user.target
`, unitName, user, user, workDir, execStart, envLines)

	path := fmt.Sprintf("/etc/systemd/system/%s.service", unitName)
	if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
		return err
	}

	RunCommand(ctx, "systemctl", "daemon-reload")
	RunCommand(ctx, "systemctl", "enable", unitName)
	_, err := RunCommand(ctx, "systemctl", "restart", unitName)
	return err
}

// DeleteSystemdUnit is the name-explicit pair for CreateSystemdUnit.
func DeleteSystemdUnit(ctx context.Context, unitName string) error {
	RunCommand(ctx, "systemctl", "stop", unitName)
	RunCommand(ctx, "systemctl", "disable", unitName)
	os.Remove(fmt.Sprintf("/etc/systemd/system/%s.service", unitName))
	_, err := RunCommand(ctx, "systemctl", "daemon-reload")
	return err
}

func AtomicSymlinkSwitch(currentLink, newReleasePath string) error {
	tmpLink := currentLink + ".tmp"
	os.Remove(tmpLink)
	if err := os.Symlink(newReleasePath, tmpLink); err != nil {
		return err
	}
	return os.Rename(tmpLink, currentLink)
}
