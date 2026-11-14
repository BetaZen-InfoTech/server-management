package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParseBZBackupLocalDir guards the DR-directory detection against the shell
// forms that are legal in the `.`-sourced backup.conf (export prefix, quotes,
// inline comment, last-wins) — the bug where any of these made the panel read
// the default directory instead of the operator's configured one.
func TestParseBZBackupLocalDir(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"BZ_BACKUP_LOCAL_DIR=/mnt/dr", "/mnt/dr"},
		{"export BZ_BACKUP_LOCAL_DIR=/mnt/dr", "/mnt/dr"},
		{`BZ_BACKUP_LOCAL_DIR="/mnt/dr"`, "/mnt/dr"},
		{"BZ_BACKUP_LOCAL_DIR=/mnt/dr # off-array", "/mnt/dr"},
		{"export BZ_BACKUP_LOCAL_DIR='/mnt/dr'  # note", "/mnt/dr"},
		{"# BZ_BACKUP_LOCAL_DIR=/commented", ""},
		{"OTHER=1\nBZ_BACKUP_LOCAL_DIR=/mnt/x", "/mnt/x"},
		{"BZ_BACKUP_LOCAL_DIR=/a\nBZ_BACKUP_LOCAL_DIR=/b", "/b"}, // last assignment wins
		{"BZ_BACKUP_RETENTION_COUNT=3", ""},                     // unrelated key
	}
	for _, c := range cases {
		if got := parseBZBackupLocalDir(strings.NewReader(c.in)); got != c.want {
			t.Errorf("parseBZBackupLocalDir(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

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

// TestScanDRBackupsMulti covers the work/ staging dir surfacing: a completed
// bundle in the local dir is Incomplete=false, a leftover in work/ is
// Incomplete=true, and a name present in both is listed once (completed wins).
func TestScanDRBackupsMulti(t *testing.T) {
	complete := t.TempDir()
	work := t.TempDir()
	mk := func(dir, name string, size int, mod time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Now().Add(-3 * time.Hour)
	t1 := time.Now().Add(-2 * time.Hour)
	t2 := time.Now().Add(-1 * time.Hour)
	mk(complete, "bzpanel-dr-host-20260101-000000.tar.gz.enc", 100, t0)       // completed
	mk(work, "bzpanel-dr-host-20260102-000000.tar.gz", 200, t1)              // incomplete leftover
	mk(complete, "bzpanel-dr-host-20260103-000000.tar.gz.enc", 300, t2)       // completed, newest
	// Same name in both dirs → completed copy wins, listed once.
	mk(work, "bzpanel-dr-host-20260103-000000.tar.gz.enc", 999, t2)

	got, err := scanDRBackupsMulti(complete, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d bundles; want 3 (%+v)", len(got), got)
	}
	// Newest first.
	if got[0].Name != "bzpanel-dr-host-20260103-000000.tar.gz.enc" {
		t.Errorf("first = %q; want the 20260103 bundle", got[0].Name)
	}
	byName := map[string]DRBackup{}
	for _, b := range got {
		byName[b.Name] = b
	}
	if byName["bzpanel-dr-host-20260103-000000.tar.gz.enc"].Incomplete {
		t.Error("deduped 20260103 should be the completed copy (Incomplete=false)")
	}
	if byName["bzpanel-dr-host-20260103-000000.tar.gz.enc"].Size != 300 {
		t.Error("deduped 20260103 should keep the completed dir's size (300)")
	}
	if byName["bzpanel-dr-host-20260101-000000.tar.gz.enc"].Incomplete {
		t.Error("completed bundle wrongly flagged Incomplete")
	}
	if !byName["bzpanel-dr-host-20260102-000000.tar.gz"].Incomplete {
		t.Error("work/ leftover not flagged Incomplete")
	}
}

// TestScanDRBackupsMultiSameDir: when work==complete (or empty), no double list.
func TestScanDRBackupsMultiSameDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bzpanel-dr-host-20260101-000000.tar.gz"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, work := range []string{dir, ""} {
		got, err := scanDRBackupsMulti(dir, work)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("work=%q: got %d; want 1", work, len(got))
		}
		if got[0].Incomplete {
			t.Errorf("work=%q: single bundle wrongly Incomplete", work)
		}
	}
}

func TestParseBZBackupVarWorkDir(t *testing.T) {
	in := "BZ_BACKUP_LOCAL_DIR=/mnt/dr\nexport BZ_BACKUP_WORK_DIR='/mnt/stage'  # staging"
	if got := parseBZBackupVar(strings.NewReader(in), "BZ_BACKUP_WORK_DIR"); got != "/mnt/stage" {
		t.Errorf("parseBZBackupVar(WORK_DIR) = %q; want /mnt/stage", got)
	}
	if got := parseBZBackupVar(strings.NewReader(in), "BZ_BACKUP_LOCAL_DIR"); got != "/mnt/dr" {
		t.Errorf("parseBZBackupVar(LOCAL_DIR) = %q; want /mnt/dr", got)
	}
}

// TestDeleteDRBundleInWork: a bundle present only in the second (work) dir is
// deleted, and a bad name is still rejected before any dir is touched.
func TestDeleteDRBundleInWork(t *testing.T) {
	complete := t.TempDir()
	work := t.TempDir()
	name := "bzpanel-dr-host-20260102-000000.tar.gz"
	for _, sfx := range []string{"", ".manifest.json", ".sha256"} {
		if err := os.WriteFile(filepath.Join(work, name+sfx), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := deleteDRBundleIn([]string{complete, work}, "../../etc/passwd"); err == nil {
		t.Fatal("accepted a traversal name")
	}
	if err := deleteDRBundleIn([]string{complete, work}, name); err != nil {
		t.Fatalf("deleteDRBundleIn(work): %v", err)
	}
	for _, sfx := range []string{"", ".manifest.json", ".sha256"} {
		if _, err := os.Stat(filepath.Join(work, name+sfx)); !os.IsNotExist(err) {
			t.Errorf("%q should have been deleted from work dir", name+sfx)
		}
	}
	if err := deleteDRBundleIn([]string{complete, work}, name); err == nil {
		t.Fatal("second delete of a now-missing bundle should error")
	}
}

func TestDRBundlePathInWork(t *testing.T) {
	complete := t.TempDir()
	work := t.TempDir()
	name := "bzpanel-dr-host-20260102-000000.tar.gz"
	if err := os.WriteFile(filepath.Join(work, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := drBundlePathIn([]string{complete, work}, name)
	if err != nil {
		t.Fatalf("drBundlePathIn(work): %v", err)
	}
	if p != filepath.Join(work, name) {
		t.Errorf("path = %q; want the work-dir path", p)
	}
	// Completed dir wins when the same name exists in both.
	if err := os.WriteFile(filepath.Join(complete, name), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err = drBundlePathIn([]string{complete, work}, name)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(complete, name) {
		t.Errorf("path = %q; want the completed-dir path (wins over work)", p)
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
