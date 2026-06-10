package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// certbotMu serializes every certbot invocation this process makes.
// certbot holds a global lock across /etc/letsencrypt, /var/lib/letsencrypt
// and /var/log/letsencrypt; a second concurrent run dies immediately with
// "Another instance of Certbot is already running". The panel fires certbot
// from many unrelated places — a bulk SSL job, a single-domain issue, the
// panel-domain SSL install, a mail-suite install, a server transfer — so we
// funnel them ALL through one mutex. That makes panel-vs-panel collisions
// impossible; the retry loop in runCertbotLong additionally rides out a
// collision with an EXTERNAL certbot (the twice-daily systemd renew timer or
// an operator's manual run) and the intermittent "connection reset" this box
// sometimes gets reaching the ACME API over a flaky route.
var certbotMu sync.Mutex

// certbotRetryMarkers are substrings (matched case-insensitively against
// certbot's combined error + stdout + stderr) that mark a TRANSIENT failure
// worth retrying: global-lock contention and flaky network paths to the ACME
// server. Anything NOT on this list — a real HTTP-01 "some challenges have
// failed", a rate-limit, an unresolvable domain — is returned immediately so
// we never burn Let's Encrypt rate-limit budget retrying a deterministic
// failure.
var certbotRetryMarkers = []string{
	"another instance of certbot is already running",
	"connection reset by peer",
	"connection aborted",
	"connectionreseterror",
	"max retries exceeded",
	"connection refused",
	"temporary failure in name resolution",
	"timed out",
	"read timed out",
	"service temporarily unavailable",
	"the service is temporarily down",
}

func certbotErrRetryable(s string) bool {
	s = strings.ToLower(s)
	for _, m := range certbotRetryMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// runCertbot runs `certbot <args...>` under the process-wide lock with a
// 5-minute per-attempt timeout and transient-failure retry. Every certbot
// call in this package goes through here (or runCertbotLong).
func runCertbot(ctx context.Context, args ...string) (*CommandResult, error) {
	return runCertbotLong(ctx, 5*time.Minute, args...)
}

// runCertbotLong is runCertbot with a caller-chosen per-attempt timeout —
// used by the mail-suite `--nginx` install, which can take longer.
func runCertbotLong(ctx context.Context, timeout time.Duration, args ...string) (*CommandResult, error) {
	certbotMu.Lock()
	defer certbotMu.Unlock()

	const maxAttempts = 6
	backoff := 5 * time.Second
	var res *CommandResult
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err = RunCommandLong(ctx, timeout, "certbot", args...)
		if err == nil {
			return res, nil
		}
		combined := err.Error()
		if res != nil {
			combined += " " + res.Output + " " + res.Error
		}
		if !certbotErrRetryable(combined) || attempt == maxAttempts {
			return res, err
		}
		// Transient — wait out the other certbot / network blip and retry,
		// honouring caller cancellation (bulk-job timeout, operator cancel).
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 40*time.Second {
			backoff *= 2
		}
	}
	return res, err
}

// RunCertbot is the exported entry for callers in other packages that build
// their own certbot argument list (e.g. the server-transfer SSL step and the
// panel-domain force-renewal). Same serialization + transient-retry as the
// in-package paths — never construct a raw RunCommand(ctx, "certbot", ...)
// outside this file.
func RunCertbot(ctx context.Context, args ...string) (*CommandResult, error) {
	return runCertbot(ctx, args...)
}

// ensureAcmeWebroot makes sure the webroot certbot writes its HTTP-01
// challenge files into exists and is world-readable. Nginx serves
// /.well-known/acme-challenge/ out of this directory (see nginx.go's
// AcmeChallengeRoot). Idempotent — install.sh creates the directory at
// bootstrap but a freshly rebuilt box or a user who wiped /var/www by
// hand still needs this to work on first certbot call.
func ensureAcmeWebroot(ctx context.Context) {
	RunCommand(ctx, "install", "-d", "-m", "0755", AcmeChallengeRoot+"/.well-known/acme-challenge")
}

// IssueLetsEncrypt requests (or reuses) a Let's Encrypt certificate
// for domain + additionalDomains. Uses --webroot HTTP-01 because it's
// stateless: the ACME client just drops a file in /var/www/certbot and
// lets nginx serve it. No nginx config mutation, no "No such
// authorization" surprises if a previous --nginx run was interrupted,
// and it works identically for domains whose vhost is PHP-FPM, static,
// reverse proxy, or even a project vhost.
//
// Wildcard certs can't use HTTP-01 (LE requires DNS-01), so the
// wildcard path still goes through --manual. Upstream callers are
// expected to handle the interactive TXT-record prompt themselves.
func IssueLetsEncrypt(ctx context.Context, domain, email string, additionalDomains []string, wildcard bool) error {
	if wildcard {
		args := []string{"certonly", "--manual", "--preferred-challenges", "dns", "--non-interactive", "--agree-tos", "-m", email, "-d", fmt.Sprintf("*.%s", domain), "-d", domain}
		_, err := runCertbot(ctx, args...)
		return err
	}

	ensureAcmeWebroot(ctx)
	args := []string{
		"certonly", "--webroot", "-w", AcmeChallengeRoot,
		"--non-interactive", "--agree-tos",
		"--cert-name", domain,
		"-m", email, "-d", domain,
	}
	for _, d := range additionalDomains {
		args = append(args, "-d", d)
	}
	_, err := runCertbot(ctx, args...)
	return err
}

func RenewCertificate(ctx context.Context, domain string) error {
	_, err := runCertbot(ctx, "renew", "--cert-name", domain, "--force-renewal", "--quiet")
	if err != nil {
		return err
	}
	return ReloadNginx(ctx)
}

// IssueLetsEncryptForced is the reissue path: it ALWAYS makes certbot
// mint a fresh certificate, even when a valid cert already exists at
// /etc/letsencrypt/live/<domain>/. The plain IssueLetsEncrypt path
// short-circuits on existing certs (because LE rate-limits 5 dup
// certs/week per registered domain, and the standard renewal cron
// keeps them fresh), but operators sometimes need a brand-new cert
// right now — typically because:
//
//   - the private key was exposed and they want to rotate it
//   - certbot's --expand failed earlier and the live cert is missing
//     a SAN that the panel thinks is there
//   - a transfer brought in a stale lineage and the operator wants
//     a clean issuance under THIS server's account
//
// Works for both fresh (no existing cert) and existing-cert cases:
// `certonly --force-renewal` issues normally on first run and forces
// a re-issue when a cert with this --cert-name is already on disk.
// Wildcards still go through the --manual DNS-01 flow because LE
// won't sign wildcards via HTTP-01.
func IssueLetsEncryptForced(ctx context.Context, domain, email string, additionalDomains []string, wildcard bool) error {
	if wildcard {
		args := []string{
			"certonly", "--manual", "--preferred-challenges", "dns",
			"--non-interactive", "--agree-tos", "--force-renewal",
			"-m", email,
			"-d", fmt.Sprintf("*.%s", domain), "-d", domain,
		}
		_, err := runCertbot(ctx, args...)
		return err
	}

	ensureAcmeWebroot(ctx)
	args := []string{
		"certonly", "--webroot", "-w", AcmeChallengeRoot,
		"--non-interactive", "--agree-tos", "--force-renewal",
		"--cert-name", domain,
		"-m", email, "-d", domain,
	}
	for _, d := range additionalDomains {
		args = append(args, "-d", d)
	}
	_, err := runCertbot(ctx, args...)
	return err
}

func RevokeCertificate(ctx context.Context, domain string) error {
	_, err := runCertbot(ctx, "revoke", "--cert-name", domain, "--non-interactive")
	return err
}

func GetCertInfo(ctx context.Context, domain string) (string, error) {
	result, err := runCertbot(ctx, "certificates", "--cert-name", domain)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Output), nil
}

// IssueLetsEncryptMulti issues (or expands) a single Let's Encrypt certificate
// that covers primary + aliases. The cert file lives under
//   /etc/letsencrypt/live/<primary>/
// regardless of how many aliases are added, because --cert-name is pinned to
// primary. Uses --expand so re-running with a new alias list updates the same
// cert in place instead of failing on the "cert already exists" check.
//
// Reloads nginx on success so the new cert is picked up by the existing vhost.
func IssueLetsEncryptMulti(ctx context.Context, primary string, aliases []string, email string) error {
	primary = strings.TrimSpace(primary)
	if primary == "" {
		return fmt.Errorf("primary domain is required")
	}
	ensureAcmeWebroot(ctx)
	args := []string{
		"certonly", "--webroot", "-w", AcmeChallengeRoot,
		"--non-interactive", "--agree-tos", "--expand",
		"--cert-name", primary,
		"-m", email,
		"-d", primary,
	}
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" || a == primary {
			continue
		}
		args = append(args, "-d", a)
	}
	if _, err := runCertbot(ctx, args...); err != nil {
		return err
	}
	return ReloadNginx(ctx)
}
