package services

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tests for the v3.1.54 Vue support. Two presets added:
//   - "vue-vite" : Vue 3 + Vite static SPA (mirror of react-vite)
//   - "nuxt"    : Nuxt 3 node SSR (mirror of nextjs)
//
// These tests guard the smoke-test shape so the WHM Deploy Software
// "Framework preset" dropdown surfaces working entries — a regression
// that breaks any of them silently turns Vue into an undeployable
// option in the UI.

func TestVueVitePreset_ShapeAndScaffold(t *testing.T) {
	p, ok := lookupPreset("vue-vite")
	if !ok {
		t.Fatal("vue-vite preset not registered — UI dropdown won't list Vue")
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"AppType", p.AppType, "static"},
		{"IsStatic", p.IsStatic, true},
		{"StaticDir", p.StaticDir, "dist"},
		{"DefaultPort", p.DefaultPort, 0},
		{"Label", p.Label, "Vue 3 + Vite (static)"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("vue-vite.%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !strings.Contains(p.BuildCmd, "npm run build") {
		t.Errorf("vue-vite.BuildCmd = %q, want it to run `npm run build`", p.BuildCmd)
	}
	if !strings.Contains(p.InstallCmd, "npm install") {
		t.Errorf("vue-vite.InstallCmd = %q, want it to run `npm install`", p.InstallCmd)
	}
	if p.StartCmd != "" {
		t.Errorf("vue-vite.StartCmd = %q, want empty (static SPA has no systemd unit)", p.StartCmd)
	}

	// Scaffold sanity — every Vue scaffold must produce a buildable project.
	for _, f := range []string{"package.json", "vite.config.js", "index.html", "src/main.js", "src/App.vue"} {
		if _, ok := p.Scaffold[f]; !ok {
			t.Errorf("vue-vite scaffold missing %s — first build will fail", f)
		}
	}
	// package.json must declare vue + vite + @vitejs/plugin-vue, otherwise
	// `npm run build` blows up with "module not found".
	pkg, _ := p.Scaffold["package.json"]
	var pj map[string]any
	if err := json.Unmarshal([]byte(pkg), &pj); err != nil {
		t.Fatalf("vue-vite package.json is invalid JSON: %v", err)
	}
	deps, _ := pj["dependencies"].(map[string]any)
	devDeps, _ := pj["devDependencies"].(map[string]any)
	for _, dep := range []string{"vue"} {
		if _, ok := deps[dep]; !ok {
			t.Errorf("vue-vite package.json missing dependency %q", dep)
		}
	}
	for _, dep := range []string{"vite", "@vitejs/plugin-vue"} {
		if _, ok := devDeps[dep]; !ok {
			t.Errorf("vue-vite package.json missing devDependency %q", dep)
		}
	}

	// vite.config must load @vitejs/plugin-vue or .vue files won't compile.
	cfg, _ := p.Scaffold["vite.config.js"]
	if !strings.Contains(cfg, "@vitejs/plugin-vue") {
		t.Errorf("vue-vite vite.config.js missing @vitejs/plugin-vue import — .vue SFCs won't build")
	}
	// index.html must load src/main.js
	html, _ := p.Scaffold["index.html"]
	if !strings.Contains(html, `src="/src/main.js"`) {
		t.Errorf("vue-vite index.html doesn't reference /src/main.js — Vite will produce an empty bundle")
	}
	// App.vue must be a valid SFC (<template> + <script setup>).
	app, _ := p.Scaffold["src/App.vue"]
	if !strings.Contains(app, "<template>") || !strings.Contains(app, "<script setup>") {
		t.Errorf("vue-vite src/App.vue not a valid SFC — missing <template> or <script setup>")
	}
}

func TestNuxtPreset_ShapeAndScaffold(t *testing.T) {
	p, ok := lookupPreset("nuxt")
	if !ok {
		t.Fatal("nuxt preset not registered — UI dropdown won't list Nuxt")
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"AppType", p.AppType, "node"},
		{"IsStatic", p.IsStatic, false},
		{"DefaultPort", p.DefaultPort, 3000},
		{"Label", p.Label, "Nuxt 3 (Vue SSR)"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("nuxt.%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !strings.Contains(p.BuildCmd, "npm run build") {
		t.Errorf("nuxt.BuildCmd = %q, want `npm run build`", p.BuildCmd)
	}
	// Nuxt 3 outputs a self-contained node server here; the start command
	// MUST exec it directly because the package.json `start` script is
	// optional (operators may edit it away).
	if !strings.Contains(p.StartCmd, ".output/server/index.mjs") {
		t.Errorf("nuxt.StartCmd = %q, want it to exec .output/server/index.mjs", p.StartCmd)
	}

	// Scaffold sanity.
	for _, f := range []string{"package.json", "nuxt.config.ts", "app.vue"} {
		if _, ok := p.Scaffold[f]; !ok {
			t.Errorf("nuxt scaffold missing %s — first build will fail", f)
		}
	}
	pkg, _ := p.Scaffold["package.json"]
	var pj map[string]any
	if err := json.Unmarshal([]byte(pkg), &pj); err != nil {
		t.Fatalf("nuxt package.json is invalid JSON: %v", err)
	}
	deps, _ := pj["dependencies"].(map[string]any)
	for _, dep := range []string{"nuxt", "vue"} {
		if _, ok := deps[dep]; !ok {
			t.Errorf("nuxt package.json missing dependency %q", dep)
		}
	}
	// app.vue must contain <template> (Nuxt 3's root component).
	app, _ := p.Scaffold["app.vue"]
	if !strings.Contains(app, "<template>") {
		t.Errorf("nuxt app.vue missing <template> — Nuxt won't render anything")
	}
}

// TestVuePresetsAppearInDropdown — guards that the public preset list
// (which the frontend fetches via GET /api/v1/whm/apps/presets) actually
// contains every Vue preset. A preset that's defined but somehow
// shadowed by the lookup would leave Vue invisible in the UI.
func TestVuePresetsAppearInDropdown(t *testing.T) {
	for _, key := range []string{"vue-vite", "nuxt", "vue-express"} {
		t.Run(key, func(t *testing.T) {
			p, ok := presets[key]
			if !ok {
				t.Fatalf("preset %q missing from public map — dropdown won't list it", key)
			}
			if p.Label == "" {
				t.Fatalf("preset %q has empty Label — dropdown will render a blank row", key)
			}
		})
	}
}

// TestVueExpressPreset_ShapeAndScaffold — the v3.1.55 fullstack preset.
// Express on $PORT serves /api/*; nginx serves dist/ for everything
// else when the operator picks role=fullstack on the service.
func TestVueExpressPreset_ShapeAndScaffold(t *testing.T) {
	p, ok := lookupPreset("vue-express")
	if !ok {
		t.Fatal("vue-express preset not registered — UI dropdown won't list Vue+Express")
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"AppType", p.AppType, "node"},
		{"IsStatic", p.IsStatic, false},
		{"DefaultPort", p.DefaultPort, 3000},
		{"Label", p.Label, "Vue 3 + Express (fullstack)"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("vue-express.%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !strings.Contains(p.StartCmd, "node server.js") {
		t.Errorf("vue-express.StartCmd = %q, want it to exec `node server.js`", p.StartCmd)
	}
	if !strings.Contains(p.BuildCmd, "npm run build") {
		t.Errorf("vue-express.BuildCmd = %q, want `npm run build`", p.BuildCmd)
	}

	// Scaffold sanity — needs BOTH a Vue frontend AND an Express server.
	for _, f := range []string{
		"package.json", "vite.config.js", "index.html",
		"src/main.js", "src/App.vue", "server.js",
	} {
		if _, ok := p.Scaffold[f]; !ok {
			t.Errorf("vue-express scaffold missing %s — fullstack project won't build/run", f)
		}
	}
	pkg, _ := p.Scaffold["package.json"]
	var pj map[string]any
	if err := json.Unmarshal([]byte(pkg), &pj); err != nil {
		t.Fatalf("vue-express package.json is invalid JSON: %v", err)
	}
	// ESM `import express` requires "type": "module".
	if t2, _ := pj["type"].(string); t2 != "module" {
		t.Errorf(`vue-express package.json missing "type": "module" — server.js ESM imports will fail`)
	}
	deps, _ := pj["dependencies"].(map[string]any)
	for _, dep := range []string{"vue", "express"} {
		if _, ok := deps[dep]; !ok {
			t.Errorf("vue-express package.json missing dependency %q", dep)
		}
	}
	devDeps, _ := pj["devDependencies"].(map[string]any)
	for _, dep := range []string{"vite", "@vitejs/plugin-vue"} {
		if _, ok := devDeps[dep]; !ok {
			t.Errorf("vue-express package.json missing devDependency %q", dep)
		}
	}
	// server.js must read $PORT and expose at least one /api route.
	srv, _ := p.Scaffold["server.js"]
	if !strings.Contains(srv, "process.env.PORT") {
		t.Errorf("vue-express server.js doesn't read $PORT — systemd's PORT env var will be ignored")
	}
	if !strings.Contains(srv, "'/api/") && !strings.Contains(srv, `"/api/`) {
		t.Errorf("vue-express server.js exposes no /api route — fullstack proxy will hit a 404")
	}
	// vite.config.js dev proxy is non-essential at deploy time but key
	// for local dev workflow; warn loudly if missing so a regression
	// doesn't quietly break `npm run dev`.
	cfg, _ := p.Scaffold["vite.config.js"]
	if !strings.Contains(cfg, "proxy") || !strings.Contains(cfg, "/api") {
		t.Errorf("vue-express vite.config.js missing /api proxy — `npm run dev` won't reach Express")
	}
}
