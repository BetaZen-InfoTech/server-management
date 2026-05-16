//go:build smoke

package services

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestVueExpressPreset_RealNpmBuildAndBoot — gated smoke for the
// v3.1.55 fullstack preset. Scaffolds, installs deps, builds the Vue
// frontend, then ACTUALLY BOOTS server.js and hits /api/hello to
// prove the Express backend serves the route the scaffold UI calls.
//
// Run with:
//
//	go test -tags=smoke -run TestVueExpressPreset_RealNpmBuildAndBoot \
//	  -v ./internal/services/...
func TestVueExpressPreset_RealNpmBuildAndBoot(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not on PATH — skipping real-build smoke")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH — skipping real-build smoke")
	}
	p, ok := lookupPreset("vue-express")
	if !ok {
		t.Fatal("vue-express preset missing")
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

	// Build pass criteria — dist/ + dist/assets/*.js
	if _, err := os.Stat(filepath.Join(dir, "dist", "index.html")); err != nil {
		t.Fatalf("dist/index.html missing — frontend build failed: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "dist", "assets", "*.js"))
	if len(matches) == 0 {
		t.Fatal("no dist/assets/*.js — Vue frontend bundle wasn't emitted")
	}
	t.Logf("✓ frontend built: %s", filepath.Base(matches[0]))

	// Boot the Express backend on a free port and hit /api/hello.
	port := freePort(t)
	t.Logf("$ PORT=%d node server.js (background)", port)
	cmd := exec.Command("node", "server.js")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("node server.js failed to start: %v", err)
	}
	go io.Copy(io.Discard, stdout)
	go io.Copy(io.Discard, stderr)
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// Poll the API up to 10s for the server to be ready.
	url := fmt.Sprintf("http://127.0.0.1:%d/api/hello", port)
	var body string
	var statusCode int
	for i := 0; i < 50; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		statusCode = resp.StatusCode
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body = string(b)
		if statusCode == 200 {
			break
		}
	}
	if statusCode != 200 {
		t.Fatalf("GET %s did not return 200 within 10s (last status=%d body=%q)", url, statusCode, body)
	}
	if !strings.Contains(body, "sp-demo-vue-express") {
		t.Fatalf("/api/hello body missing scaffold marker: %s", body)
	}
	t.Logf("✓ backend boots, /api/hello returned 200: %s", body)
}

// freePort returns a TCP port the OS just confirmed is free. Race-safe
// enough for a single-test smoke — we open then immediately close, then
// pass the port to the child process which re-binds.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("can't find free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
