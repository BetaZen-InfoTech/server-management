package services

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Tests the stdlib-backed compress helpers across three cases operators
// hit in the File Manager: a single file, a single folder, and a mix
// of files + folders. Goal is to catch regressions the moment they
// land, since the original bug (compress failing because `zip` wasn't
// installed) got to production unnoticed.

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setupTree(t *testing.T) (string, []string) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "loose.txt"), "loose")
	writeFile(t, filepath.Join(root, "project", "package.json"), `{"name":"demo"}`)
	writeFile(t, filepath.Join(root, "project", "src", "index.js"), "console.log('hi');\n")
	writeFile(t, filepath.Join(root, "project", ".env"), "SECRET=x\n")
	return root, []string{
		filepath.Join(root, "loose.txt"),
		filepath.Join(root, "project"),
	}
}

func readZipNames(t *testing.T, archivePath string) []string {
	t.Helper()
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

func readTarGzNames(t *testing.T, archivePath string) []string {
	t.Helper()
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open tar.gz: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	return names
}

func TestCompressZip_FileAndFolder(t *testing.T) {
	_, srcs := setupTree(t)
	out := filepath.Join(t.TempDir(), "mixed.zip")
	if err := compressZip(srcs, out); err != nil {
		t.Fatalf("compressZip: %v", err)
	}
	names := readZipNames(t, out)
	want := []string{
		"loose.txt",
		"project/.env",
		"project/package.json",
		"project/src/",
		"project/src/index.js",
	}
	// Directory entries are optional; assert every "want" file is present.
	for _, w := range want {
		if strings.HasSuffix(w, "/") {
			continue
		}
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("zip missing %q; got %v", w, names)
		}
	}
}

func TestCompressZip_SingleFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "only.txt")
	writeFile(t, path, "hello")
	out := filepath.Join(t.TempDir(), "one.zip")
	if err := compressZip([]string{path}, out); err != nil {
		t.Fatalf("compressZip: %v", err)
	}
	names := readZipNames(t, out)
	if len(names) != 1 || names[0] != "only.txt" {
		t.Fatalf("expected [only.txt], got %v", names)
	}
}

func TestCompressTarGz_FileAndFolder(t *testing.T) {
	_, srcs := setupTree(t)
	out := filepath.Join(t.TempDir(), "mixed.tar.gz")
	if err := compressTarGz(context.Background(), srcs, out); err != nil {
		t.Fatalf("compressTarGz: %v", err)
	}
	names := readTarGzNames(t, out)
	for _, w := range []string{"loose.txt", "project/package.json", "project/src/index.js", "project/.env"} {
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tar missing %q; got %v", w, names)
		}
	}
}

func TestCompressTarGz_SingleFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "only.txt")
	writeFile(t, path, "hello")
	out := filepath.Join(t.TempDir(), "one.tar.gz")
	if err := compressTarGz(context.Background(), []string{path}, out); err != nil {
		t.Fatalf("compressTarGz: %v", err)
	}
	names := readTarGzNames(t, out)
	if len(names) != 1 || names[0] != "only.txt" {
		t.Fatalf("expected [only.txt], got %v", names)
	}
}

// Round-trip: compress → extract → assert file contents match.
func TestCompressZip_RoundTrip(t *testing.T) {
	_, srcs := setupTree(t)
	archive := filepath.Join(t.TempDir(), "round.zip")
	if err := compressZip(srcs, archive); err != nil {
		t.Fatalf("compressZip: %v", err)
	}
	dest := t.TempDir()
	if err := extractZip(archive, dest); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	// loose.txt should appear at dest root.
	if b, err := os.ReadFile(filepath.Join(dest, "loose.txt")); err != nil {
		t.Fatalf("read loose.txt: %v", err)
	} else if string(b) != "loose" {
		t.Errorf("loose content: got %q", b)
	}
	// project/src/index.js should keep its payload.
	p := filepath.Join(dest, "project", "src", "index.js")
	if b, err := os.ReadFile(p); err != nil {
		t.Fatalf("read index.js: %v", err)
	} else if !strings.Contains(string(b), "console.log") {
		t.Errorf("index.js content unexpected: %q", b)
	}
}
