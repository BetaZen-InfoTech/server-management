package agent

import (
	"context"
	"fmt"
	"strings"
)

func IssueLetsEncrypt(ctx context.Context, domain, email string, additionalDomains []string, wildcard bool) error {
	args := []string{"certonly", "--nginx", "--non-interactive", "--agree-tos", "-m", email, "-d", domain}
	for _, d := range additionalDomains {
		args = append(args, "-d", d)
	}
	if wildcard {
		args = []string{"certonly", "--manual", "--preferred-challenges", "dns", "--non-interactive", "--agree-tos", "-m", email, "-d", fmt.Sprintf("*.%s", domain), "-d", domain}
	}
	_, err := RunCommand(ctx, "certbot", args...)
	return err
}

func RenewCertificate(ctx context.Context, domain string) error {
	_, err := RunCommand(ctx, "certbot", "renew", "--cert-name", domain, "--force-renewal", "--quiet")
	if err != nil {
		return err
	}
	return ReloadNginx(ctx)
}

func RevokeCertificate(ctx context.Context, domain string) error {
	_, err := RunCommand(ctx, "certbot", "revoke", "--cert-name", domain, "--non-interactive")
	return err
}

func GetCertInfo(ctx context.Context, domain string) (string, error) {
	result, err := RunCommand(ctx, "certbot", "certificates", "--cert-name", domain)
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
	args := []string{
		"certonly", "--nginx", "--non-interactive", "--agree-tos", "--expand",
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
	if _, err := RunCommand(ctx, "certbot", args...); err != nil {
		return err
	}
	return ReloadNginx(ctx)
}
