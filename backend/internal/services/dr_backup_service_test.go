package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidDRName(t *testing.T) {
	valid := []string{
		"bzpanel-dr-srv1914283.hstgr.cloud-20260827-034041.tar.gz.enc",
		"bzpanel-dr-host-20260101-000000.tar.gz",
	}
	for _, n := range valid {
		if !validDRName(n) {
			t.Errorf("validDRName(%q) = false; want true", n)
		}
	}
	// Traversal / wrong-prefix / wrong-suffix / separators must all be rejected.
	bad := []string{
		"",
		"../../etc/passwd",
		"bzpanel-dr-../../../etc/shadow.tar.gz",
		"bzpanel-dr-x/y.tar.gz",
		`bzpanel-dr-x\y.tar.gz`,
		"passwd",
		"bzpanel-dr-x-20260101.tar.gz.enc.manifest.json", // sidecar, not a bundle
		"bzpanel-dr-x.sha256",
		"random-20260101.tar.gz",  // wrong prefix
		"bzpanel-dr-x.zip",         // wrong suffix
	}
	for _, n := range bad {
		if validDRName(n) {
			t.Errorf("validDRName(%q) = true; want false", n)
		}
	}
}

func TestDRHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:              "0 B",
		512:            "512 B",
		1024:           "1.0 KB",
		1536:           "1.5 KB",
		1073741824:     "1.0 GB",
		32984167888:    "30.7 GB",
	}
	for in, want := range cases {
		if got := drHumanSize(in); got != want {
			t.Errorf("drHumanSize(%d) = %q; want %q", in, got, want)
		}
	}
}

func TestScanDRBackups(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int, mod time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	write("bzpanel-dr-host-20260101-000000.tar.gz.enc", 100, older)
	write("bzpanel-dr-host-20260102-000000.tar.gz", 200, newer)
	// Sidecars + noise that must NOT be listed.
	write("bzpanel-dr-host-20260101-000000.tar.gz.enc.manifest.json", 10, older)
	write("bzpanel-dr-host-20260101-000000.tar.gz.enc.sha256", 10, older)
	write("some-other-file.txt", 10, newer)
	write("bzpanel-dr-host-20260103-000000.zip", 10, newer) // wrong suffix

	got, err := scanDRBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("scanDRBackups returned %d bundles; want 2 (%+v)", len(got), got)
	}
	// Newest first.
	if got[0].Name != "bzpanel-dr-host-20260102-000000.tar.gz" {
		t.Errorf("first (newest) = %q; want the 20260102 .tar.gz", got[0].Name)
	}
	if got[0].Encrypted {
		t.Errorf("20260102 .tar.gz reported encrypted; want false")
	}
	if !got[1].Encrypted {
		t.Errorf("20260101 .tar.gz.enc reported not encrypted; want true")
	}
	if got[1].Size != 100 || got[0].Size != 200 {
		t.Errorf("sizes = %d/%d; want 200/100", got[0].Size, got[1].Size)
	}
}

func TestScanDRBackupsMissingDir(t *testing.T) {
	got, err := scanDRBackups(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing dir should yield 0 bundles, got %d", len(got))
	}
}

func TestDeleteDRBundle(t *testing.T) {
	dir := t.TempDir()
	name := "bzpanel-dr-host-20260101-000000.tar.gz.enc"
	mk := func(n string) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk(name)
	mk(name + ".manifest.json")
	mk(name + ".sha256")
	keep := "bzpanel-dr-host-20260102-000000.tar.gz"
	mk(keep)

	// Traversal / bad name rejected before any disk touch.
	if err := deleteDRBundle(dir, "../../etc/passwd"); err == nil {
		t.Fatal("deleteDRBundle accepted a traversal name")
	}
	// Missing bundle → error.
	if err := deleteDRBundle(dir, "bzpanel-dr-host-19990101-000000.tar.gz"); err == nil {
		t.Fatal("deleteDRBundle accepted a non-existent bundle")
	}

	if err := deleteDRBundle(dir, name); err != nil {
		t.Fatalf("deleteDRBundle: %v", err)
	}
	for _, gone := range []string{name, name + ".manifest.json", name + ".sha256"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%q should have been deleted", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
		t.Errorf("unrelated bundle %q was wrongly deleted", keep)
	}
}

func TestDRBundlePathTraversal(t *testing.T) {
	dir := t.TempDir()
	if _, err := drBundlePath(dir, "../secret.tar.gz"); err == nil {
		t.Fatal("drBundlePath accepted a traversal name")
	}
	name := "bzpanel-dr-host-20260101-000000.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := drBundlePath(dir, name)
	if err != nil {
		t.Fatalf("drBundlePath(valid): %v", err)
	}
	if p != filepath.Join(dir, name) {
		t.Errorf("drBundlePath = %q; want %q", p, filepath.Join(dir, name))
	}
}
