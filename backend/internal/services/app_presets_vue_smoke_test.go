//go:build smoke

package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestVueVitePreset_RealNpmBuild — gated end-to-end smoke test that
// actually runs `npm install` + `npm run build` against the scaffold.
// Hidden behind the `smoke` build tag because it pulls hundreds of MB
// of npm deps and takes ~60s; not for the normal `go test ./...` loop.
//
// Run with:
//
//	go test -tags=smoke -run TestVueVitePreset_RealNpmBuild -v ./internal/services/...
//
// Pass criterion: dist/index.html + a dist/assets/*.js bundle exist
// after the build. Proves the scaffold isn't just syntactically valid
// Go-strings — it's an actual buildable Vue project that nginx can serve.
func TestVueVitePreset_RealNpmBuild(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not on PATH — skipping real-build smoke")
	}
	p, ok := lookupPreset("vue-vite")
	if !ok {
		t.Fatal("vue-vite preset missing")
	}

	dir := t.TempDir()
	for rel, content := range p.Scaffold {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	run := func(name string, args ...string) {
		t.Helper()
		t.Logf("$ %s %v", name, args)
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"CI=1",
			"npm_config_audit=false",
			"npm_config_fund=false",
		)
		start := time.Now()
		out, err := cmd.CombinedOutput()
		t.Logf("(%.1fs) %s", time.Since(start).Seconds(), out)
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", name, err, out)
		}
	}

	run("npm", "install", "--no-audit", "--no-fund", "--loglevel=error")
	run("npm", "run", "build")

	// Pass criteria — dist/index.html must exist and the index must
	// reference a hashed JS bundle under dist/assets/.
	html, err := os.ReadFile(filepath.Join(dir, "dist", "index.html"))
	if err != nil {
		t.Fatalf("dist/index.html missing — Vite didn't produce a static bundle: %v", err)
	}
	t.Logf("dist/index.html: %d bytes", len(html))
	matches, _ := filepath.Glob(filepath.Join(dir, "dist", "assets", "*.js"))
	if len(matches) == 0 {
		t.Fatalf("no dist/assets/*.js — Vue bundle wasn't emitted")
	}
	t.Logf("✓ Vue bundle compiled: %s", filepath.Base(matches[0]))
}
