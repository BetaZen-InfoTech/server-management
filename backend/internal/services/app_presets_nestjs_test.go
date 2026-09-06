package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the v3.1.213 NestJS support:
//   - "nestjs" framework preset (AppType node, `nest build` → dist/main.js)
//   - package.json auto-detection (@nestjs/core → nestjs, keeps devDeps)
//   - migration runtime mapping (nestjs → nodejs)
//
// A NestJS deploy is distinct from generic node-express: it MUST keep
// devDependencies (nest CLI + typescript live there), build with `nest build`,
// and start the COMPILED entrypoint `node dist/main.js` — not `node server.js`.
// These guard that shape end to end.

func TestNestJSPreset_ShapeAndScaffold(t *testing.T) {
	p, ok := lookupPreset("nestjs")
	if !ok {
		t.Fatal("nestjs preset not registered — UI dropdown won't list NestJS")
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"AppType", p.AppType, "node"},
		{"IsStatic", p.IsStatic, false},
		{"DefaultPort", p.DefaultPort, 3000},
		{"Label", p.Label, "NestJS (Node)"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("nestjs.%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !strings.Contains(p.BuildCmd, "npm run build") {
		t.Errorf("nestjs.BuildCmd = %q, want `npm run build` (→ nest build)", p.BuildCmd)
	}
	// The compiled entrypoint — NOT `node server.js`. nest build emits
	// dist/main.js from src/main.ts.
	if !strings.Contains(p.StartCmd, "dist/main") {
		t.Errorf("nestjs.StartCmd = %q, want it to exec the compiled dist/main.js", p.StartCmd)
	}
	// CRITICAL: install MUST keep devDependencies. `nest build` needs
	// @nestjs/cli + typescript, both devDeps — an `--omit=dev` install would
	// strip them and the very first build would fail.
	if strings.Contains(p.InstallCmd, "--omit=dev") || strings.Contains(p.InstallCmd, "--production") {
		t.Errorf("nestjs.InstallCmd = %q, must NOT drop devDependencies (nest build needs @nestjs/cli + typescript)", p.InstallCmd)
	}
	if !strings.Contains(p.InstallCmd, "npm install") {
		t.Errorf("nestjs.InstallCmd = %q, want it to run `npm install`", p.InstallCmd)
	}

	// Scaffold sanity — a NestJS project needs its build config + a src tree.
	for _, f := range []string{
		"package.json", "nest-cli.json", "tsconfig.json", "tsconfig.build.json",
		"src/main.ts", "src/app.module.ts", "src/app.controller.ts", "src/app.service.ts",
	} {
		if _, ok := p.Scaffold[f]; !ok {
			t.Errorf("nestjs scaffold missing %s — first build/run will fail", f)
		}
	}

	// package.json must be valid JSON and declare the runtime deps + build
	// tooling, or `npm run build` / `node dist/main.js` blow up.
	pkg := p.Scaffold["package.json"]
	var pj map[string]any
	if err := json.Unmarshal([]byte(pkg), &pj); err != nil {
		t.Fatalf("nestjs package.json is invalid JSON: %v", err)
	}
	deps, _ := pj["dependencies"].(map[string]any)
	for _, dep := range []string{"@nestjs/core", "@nestjs/common", "@nestjs/platform-express", "reflect-metadata", "rxjs"} {
		if _, ok := deps[dep]; !ok {
			t.Errorf("nestjs package.json missing dependency %q", dep)
		}
	}
	devDeps, _ := pj["devDependencies"].(map[string]any)
	for _, dep := range []string{"@nestjs/cli", "typescript"} {
		if _, ok := devDeps[dep]; !ok {
			t.Errorf("nestjs package.json missing devDependency %q (needed by nest build)", dep)
		}
	}
	scripts, _ := pj["scripts"].(map[string]any)
	if b, _ := scripts["build"].(string); !strings.Contains(b, "nest build") {
		t.Errorf(`nestjs package.json scripts.build = %q, want "nest build"`, b)
	}

	// main.ts must read $PORT and bootstrap Nest, else the panel's assigned
	// port is ignored and the port-bind health check fails.
	main := p.Scaffold["src/main.ts"]
	if !strings.Contains(main, "process.env.PORT") {
		t.Errorf("nestjs src/main.ts doesn't read process.env.PORT — systemd's PORT env var is ignored")
	}
	if !strings.Contains(main, "NestFactory") || !strings.Contains(main, "app.listen") {
		t.Errorf("nestjs src/main.ts missing NestFactory bootstrap / app.listen")
	}
	// app.module.ts must wire the controller so GET / actually responds.
	mod := p.Scaffold["src/app.module.ts"]
	if !strings.Contains(mod, "AppController") {
		t.Errorf("nestjs src/app.module.ts doesn't register AppController")
	}
}

// TestNestJSPresetAppearsInDropdown — the frontend fetches the preset map via
// GET /api/v1/whm/apps/presets; guard that nestjs is actually in it.
func TestNestJSPresetAppearsInDropdown(t *testing.T) {
	p, ok := presets["nestjs"]
	if !ok {
		t.Fatal("nestjs missing from public preset map — dropdown won't list it")
	}
	if p.Label == "" {
		t.Fatal("nestjs preset has empty Label — dropdown will render a blank row")
	}
}

// TestDetectNestJS_FromPackageJSON — an imported repo whose package.json
// depends on @nestjs/core must be classified as nestjs (not the generic
// node-express bucket), keep its devDependencies, build with `npm run build`,
// and start the compiled dist/main.js.
func TestDetectNestJS_FromPackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkg := `{
  "name": "my-nest-api",
  "scripts": { "build": "nest build", "start": "nest start", "start:prod": "node dist/main" },
  "dependencies": { "@nestjs/core": "10.0.0", "@nestjs/common": "10.0.0" },
  "devDependencies": { "@nestjs/cli": "10.0.0", "typescript": "5.0.0" }
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	h := DetectPackageJSONHints(dir)

	if h.Framework != "nestjs" {
		t.Errorf("Framework = %q, want %q (must not fall into the node-express bucket)", h.Framework, "nestjs")
	}
	if h.IsStatic {
		t.Error("IsStatic = true, want false (NestJS is a long-running server, not a static SPA)")
	}
	// No lockfile present + nestjs → keep-devDeps branch → plain `npm install`.
	if strings.Contains(h.InstallCmd, "--omit=dev") || strings.Contains(h.InstallCmd, "--production") {
		t.Errorf("InstallCmd = %q, must NOT drop devDependencies for NestJS", h.InstallCmd)
	}
	if !strings.Contains(h.BuildCmd, "build") {
		t.Errorf("BuildCmd = %q, want a build step", h.BuildCmd)
	}
	if h.StartCmd != "node dist/main.js" {
		t.Errorf("StartCmd = %q, want %q (compiled entrypoint, not the dev `nest start` script)", h.StartCmd, "node dist/main.js")
	}
}

// TestNestJSRuntimeAndAppType — migration recovery resolves the runtime via
// frameworkToRuntimeKey, and vhost/port logic via resolveServiceAppType. Both
// must place nestjs in the Node bucket so a transferred NestJS service starts
// under the right runtime and gets a reverse-proxy (not a static) vhost.
func TestNestJSRuntimeAndAppType(t *testing.T) {
	if got := frameworkToRuntimeKey("nestjs"); got != "nodejs" {
		t.Errorf(`frameworkToRuntimeKey("nestjs") = %q, want "nodejs"`, got)
	}
	if got := resolveServiceAppType("nestjs", "backend"); got != "node" {
		t.Errorf(`resolveServiceAppType("nestjs","backend") = %q, want "node"`, got)
	}
}
