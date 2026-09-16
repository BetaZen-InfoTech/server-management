package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// mkAccount creates a fake certbot account dir (with regr.json) under base.
func mkAccount(t *testing.T, base, id string) {
	t.Helper()
	dir := filepath.Join(base, "acme-v02.api.letsencrypt.org", "directory", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "regr.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCertbotAccountArgs_SingleAccountNoOp(t *testing.T) {
	base := t.TempDir()
	mkAccount(t, base, "only1111")
	old := certbotAccountsDir
	certbotAccountsDir = base
	defer func() { certbotAccountsDir = old }()

	if got := certbotAccountArgs(); got != nil {
		t.Fatalf("single account must need no --account, got %v", got)
	}
}

func TestCertbotAccountArgs_PicksRenewalMajority(t *testing.T) {
	base := t.TempDir()
	renewDir := t.TempDir()
	mkAccount(t, base, "9db7new0") // newer, unused by any cert
	mkAccount(t, base, "9dfaold0") // the account every existing cert uses
	// Two renewal configs, both on 9dfaold0.
	for _, name := range []string{"a.conf", "b.conf"} {
		if err := os.WriteFile(filepath.Join(renewDir, name),
			[]byte("archive_dir = /x\naccount = 9dfaold0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldA, oldR := certbotAccountsDir, certbotRenewalGlob
	certbotAccountsDir = base
	certbotRenewalGlob = filepath.Join(renewDir, "*.conf")
	defer func() { certbotAccountsDir, certbotRenewalGlob = oldA, oldR }()

	got := certbotAccountArgs()
	if len(got) != 2 || got[0] != "--account" || got[1] != "9dfaold0" {
		t.Fatalf("must pick the account used by existing renewals (9dfaold0), got %v", got)
	}
}

func TestMaybeInjectAccount_OnlyCertonly(t *testing.T) {
	base := t.TempDir()
	mkAccount(t, base, "acctAAAA")
	mkAccount(t, base, "acctBBBB")
	oldA, oldR := certbotAccountsDir, certbotRenewalGlob
	certbotAccountsDir = base
	certbotRenewalGlob = filepath.Join(t.TempDir(), "none-*.conf") // no renewals → deterministic first
	defer func() { certbotAccountsDir, certbotRenewalGlob = oldA, oldR }()

	// certonly gets --account injected.
	got := maybeInjectAccount([]string{"certonly", "--webroot", "-d", "x.com"})
	found := false
	for i := 0; i+1 < len(got); i++ {
		if got[i] == "--account" && got[i+1] == "acctAAAA" {
			found = true
		}
	}
	if !found {
		t.Fatalf("certonly must get --account acctAAAA (lexicographically first), got %v", got)
	}

	// renew must NOT get --account (uses per-cert account).
	if r := maybeInjectAccount([]string{"renew", "--cert-name", "x.com"}); len(r) != 3 {
		t.Fatalf("renew must be left untouched, got %v", r)
	}

	// An explicit --account is never overridden.
	in := []string{"certonly", "--account", "mine1234", "-d", "x.com"}
	if r := maybeInjectAccount(in); len(r) != len(in) {
		t.Fatalf("explicit --account must be preserved, got %v", r)
	}
}

func TestIsNginxCacheDirError(t *testing.T) {
	yes := errorString(`nginx config test failed: exit status 1: [emerg] mkdir() "/var/cache/nginx/client_temp" failed (2: No such file or directory)`)
	if !isNginxCacheDirError(yes) {
		t.Fatalf("must detect the missing /var/cache/nginx mkdir failure")
	}
	no := errorString(`nginx: [emerg] could not build server_names_hash, you should increase server_names_hash_bucket_size: 64`)
	if isNginxCacheDirError(no) {
		t.Fatalf("must not misfire on an unrelated nginx error")
	}
	if isNginxCacheDirError(nil) {
		t.Fatalf("nil error must be false")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }
