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
