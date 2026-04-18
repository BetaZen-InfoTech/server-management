package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// PackageJSONHints holds the defaults we can derive from a freshly-cloned
// Node/Next.js repo without the operator having to fill them in. Fields
// stay empty when a signal isn't present — callers MUST only fill their own
// empty fields from this struct, never overwrite a user-provided value.
type PackageJSONHints struct {
	Framework  string // "nextjs" | "node-express" | "react-vite" | ""
	Port       int    // parsed from scripts.start / scripts.dev / .env
	InstallCmd string // derived from the lockfile (npm/yarn/pnpm)
	BuildCmd   string // scripts.build verbatim if present
	StartCmd   string // scripts.start verbatim if present
	// IsStatic true when the project is a Vite/CRA-style SPA: no long-running
	// process needed, just serve the built dist/ directory behind nginx.
	IsStatic bool
}

// DetectPackageJSONHints reads package.json (and nearby .env files + lockfile)
// from appDir and reports whatever it can infer. Never errors — missing or
// malformed package.json just returns a zero-valued struct.
func DetectPackageJSONHints(appDir string) PackageJSONHints {
	h := PackageJSONHints{}

	data, err := os.ReadFile(filepath.Join(appDir, "package.json"))
	if err != nil {
		return h
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Scripts         map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return h
	}

	// -------- Framework detection (ordered by specificity) ----------
	// Next.js first so repos that depend on both `next` and `react` are
	// classified as Next.js rather than as a plain static build.
	switch {
	case hasDep(pkg.Dependencies, pkg.DevDependencies, "next"):
		h.Framework = "nextjs"
	case hasDep(pkg.Dependencies, pkg.DevDependencies, "nuxt"):
		// nuxt builds to a Node server too; reuse the nextjs preset's
		// "start the built server" shape. Users can tweak post-detection.
		h.Framework = "nextjs"
	case hasDep(pkg.Dependencies, pkg.DevDependencies, "vite"):
		h.Framework = "react-vite"
		h.IsStatic = true
	case hasDep(pkg.Dependencies, pkg.DevDependencies, "react-scripts"):
		// CRA — produces a static build/ dir.
		h.Framework = "react-vite" // closest preset; the install/build/start
		h.IsStatic = true           // pattern is identical
	case hasDep(pkg.Dependencies, pkg.DevDependencies, "express"),
		hasDep(pkg.Dependencies, pkg.DevDependencies, "fastify"),
		hasDep(pkg.Dependencies, pkg.DevDependencies, "koa"),
		hasDep(pkg.Dependencies, pkg.DevDependencies, "hapi"),
		hasDep(pkg.Dependencies, pkg.DevDependencies, "@nestjs/core"):
		h.Framework = "node-express"
	case pkg.Scripts["start"] != "":
		// Fallback: a `start` script with no recognised framework is still
		// a Node server from our POV.
		h.Framework = "node-express"
	}

	// -------- Port detection -----------------------------------------
	// Try scripts.start first (what production deploys run), then
	// scripts.dev (common Next.js pattern of only setting -p in dev),
	// then .env files. The first non-zero result wins.
	for _, scriptKey := range []string{"start", "dev"} {
		if s := pkg.Scripts[scriptKey]; s != "" {
			if p := extractPortFromCmd(s); p > 0 {
				h.Port = p
				break
			}
		}
	}
	if h.Port == 0 {
		for _, name := range []string{".env", ".env.production", ".env.local", ".env.example"} {
			if p := readPortFromEnvFile(filepath.Join(appDir, name)); p > 0 {
				h.Port = p
				break
			}
		}
	}

	// -------- Install command (lockfile-aware) ----------------------
	h.InstallCmd = detectInstallCmd(appDir, h.IsStatic)

	// -------- Build / start commands -------------------------------
	if s, ok := pkg.Scripts["build"]; ok && s != "" {
		h.BuildCmd = "npm run build"
		if lockfileExists(appDir, "yarn.lock") {
			h.BuildCmd = "yarn build"
		} else if lockfileExists(appDir, "pnpm-lock.yaml") {
			h.BuildCmd = "pnpm build"
		}
	}
	if s, ok := pkg.Scripts["start"]; ok && s != "" {
		// The preset's ${PORT} substitution still applies — we only record
		// a hint here so callers that want to honor package.json exactly
		// can pick it up. For Next.js with a detected port we inject
		// -p ${PORT} so our allocated port and the process align.
		switch {
		case h.Framework == "nextjs":
			h.StartCmd = "/usr/local/bin/npx next start -p ${PORT}"
		case h.Framework == "node-express":
			// Respect whatever entry file the user's start script references.
			// Common forms: "node server.js", "node dist/index.js", "ts-node
			// src/main.ts". Use the start script verbatim so custom entry
			// points, TS runners, PM2 configs, etc. all work.
			h.StartCmd = strings.TrimSpace(s)
		}
	}

	return h
}

// hasDep checks a dep-or-devDep map for a key. Helper because ranging over
// two maps inline reads noisy at every switch case.
func hasDep(deps, devDeps map[string]string, name string) bool {
	if _, ok := deps[name]; ok {
		return true
	}
	_, ok := devDeps[name]
	return ok
}

// portFlagRe matches `-p 3000`, `--port 3000`, `--port=3000`. Non-capturing
// alternation on the flag keeps the port the only capture group so a single
// FindStringSubmatch returns the number directly.
var portFlagRe = regexp.MustCompile(`(?:^|\s)(?:-p|--port)(?:\s+|=)(\d+)`)

// portEnvPrefixRe matches PORT=3000 as either a bare shell prefix
// (`PORT=3000 node server.js`) or a cross-env invocation
// (`cross-env PORT=3000 next start`).
var portEnvPrefixRe = regexp.MustCompile(`(?:^|\s)PORT=(\d+)`)

// extractPortFromCmd returns the explicit port referenced in a shell command
// string, or 0 if no plausible port is present. Prefers PORT= env-var
// prefixes over -p/--port flags because those are closer to what a runtime
// will actually read from the environment.
func extractPortFromCmd(cmd string) int {
	if m := portEnvPrefixRe.FindStringSubmatch(cmd); len(m) > 1 {
		if p, err := strconv.Atoi(m[1]); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	if m := portFlagRe.FindStringSubmatch(cmd); len(m) > 1 {
		if p, err := strconv.Atoi(m[1]); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	return 0
}

// readPortFromEnvFile scans a .env-style file for a PORT= entry. Comments
// (`# foo`), surrounding quotes (`PORT="3000"`), and inline whitespace
// (`PORT = 3000`) are all tolerated. Returns 0 if none found.
func readPortFromEnvFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Only inspect lines that start with PORT, case-insensitively, to
		// avoid matching APP_PORT or DATABASE_PORT by accident.
		upper := strings.ToUpper(line)
		if !strings.HasPrefix(upper, "PORT=") && !strings.HasPrefix(upper, "PORT ") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		if p, err := strconv.Atoi(val); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	return 0
}

// detectInstallCmd returns the appropriate package-manager install command
// based on which lockfile is present in appDir. Defaults to npm without a
// lockfile because it's the most broadly-available runtime. For static
// builds we drop `--omit=dev` since build tools (Vite, webpack, etc.) are
// always devDeps and dropping them breaks the build step.
func detectInstallCmd(appDir string, isStatic bool) string {
	switch {
	case lockfileExists(appDir, "pnpm-lock.yaml"):
		return "pnpm install --frozen-lockfile"
	case lockfileExists(appDir, "yarn.lock"):
		return "yarn install --frozen-lockfile"
	case lockfileExists(appDir, "package-lock.json"):
		if isStatic {
			return "npm ci --no-audit --no-fund --loglevel=error"
		}
		return "npm ci --omit=dev --no-audit --no-fund --loglevel=error"
	}
	if isStatic {
		return "npm install --no-audit --no-fund --loglevel=error"
	}
	return "npm install --omit=dev --no-audit --no-fund --loglevel=error"
}

func lockfileExists(appDir, name string) bool {
	info, err := os.Stat(filepath.Join(appDir, name))
	return err == nil && !info.IsDir()
}

// applyPkgHints overlays detected hints onto the four command / port fields,
// only filling fields that are currently empty. Used by both Deploy Software
// (AddService) and the legacy /apps Deploy path so the rule for "did the
// operator override this" is exactly the same in both places.
//
// Returns a short human-readable summary of what got auto-filled so the
// caller can log it for operator visibility.
func applyPkgHints(
	hints *PackageJSONHints,
	framework, installCmd, buildCmd, startCmd *string,
	port *int,
) string {
	if hints == nil {
		return ""
	}
	var filled []string
	if *framework == "" && hints.Framework != "" {
		*framework = hints.Framework
		filled = append(filled, "framework="+hints.Framework)
	}
	if *installCmd == "" && hints.InstallCmd != "" {
		*installCmd = hints.InstallCmd
		filled = append(filled, "install_cmd")
	}
	if *buildCmd == "" && hints.BuildCmd != "" {
		*buildCmd = hints.BuildCmd
		filled = append(filled, "build_cmd")
	}
	if *startCmd == "" && hints.StartCmd != "" {
		*startCmd = hints.StartCmd
		filled = append(filled, "start_cmd")
	}
	if *port == 0 && hints.Port > 0 {
		*port = hints.Port
		filled = append(filled, fmt.Sprintf("port=%d", hints.Port))
	}
	if len(filled) == 0 {
		return ""
	}
	return "auto-detected from package.json: " + strings.Join(filled, ", ")
}
