package services

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
)

// ClearCacheResult summarizes one cache-clear.
type ClearCacheResult struct {
	Target     string   `json:"target"`
	Removed    []string `json:"removed"`
	Redeployed bool     `json:"redeployed"`
	DurationMs int64    `json:"duration_ms"`
}

// safeCacheWorkDir rejects any dir that isn't a deep path under a recognised
// install root, so a cache clear can never rm -rf a home root, /, /etc, etc.
// Allowed roots mirror where the panel actually installs code: /home/<user>/…
// (the default), plus custom InstallPath targets under /opt, /srv, /var/www.
// A minimum depth of 3 segments keeps the deletion well inside an app/service
// directory and never at a shared parent like /home or /opt.
func safeCacheWorkDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("cache clear refused: empty directory")
	}
	if !path.IsAbs(dir) {
		return fmt.Errorf("cache clear refused: %q is not absolute", dir)
	}
	if path.Clean(dir) != dir {
		return fmt.Errorf("cache clear refused: %q is not a clean path", dir)
	}
	segs := make([]string, 0, 8)
	for _, seg := range strings.Split(dir, "/") {
		if seg == ".." {
			return fmt.Errorf("cache clear refused: %q contains '..'", dir)
		}
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	if len(segs) == 0 {
		return fmt.Errorf("cache clear refused: %q is the filesystem root", dir)
	}
	tooBroad := fmt.Errorf("cache clear refused: %q is too broad", dir)
	switch {
	case segs[0] == "home":
		// /home/<user>/<area>/<name> — the real app/service shape.
		if len(segs) < 4 {
			return tooBroad
		}
	case segs[0] == "opt", segs[0] == "srv":
		// custom InstallPath like /opt/<app>/… or /srv/<app>/…
		if len(segs) < 3 {
			return tooBroad
		}
	case segs[0] == "var" && len(segs) >= 2 && segs[1] == "www":
		// /var/www/<site>/…
		if len(segs) < 3 {
			return tooBroad
		}
	default:
		return fmt.Errorf("cache clear refused: %q is not under an allowed install root (/home, /opt, /srv, /var/www)", dir)
	}
	return nil
}

// clearBuildCacheDirs removes regenerable build/dependency caches from workDir.
//
// It ONLY removes things the build step regenerates and that are never the
// artifact actually served to users: dependency trees (node_modules), tool
// caches (.turbo, .next/cache, .vite, .cache, .parcel-cache, .eslintcache,
// .pytest_cache), incremental TS build info (*.tsbuildinfo) and Python
// bytecode caches (__pycache__). It deliberately does NOT touch built OUTPUT
// (.next, dist, build, out, .svelte-kit, .nuxt): on the project surface the
// rebuild is queued/asynchronous, so if we deleted the served output here and
// the background rebuild then failed, the site would go dark with no error
// surfaced. Leaving the last-good output in place means a failed rebuild is
// non-destructive — the previous build keeps serving until a good one replaces
// it. (The build step overwrites that output on success anyway, so keeping it
// costs nothing.)
//
// NEVER removes source, .env, .git, uploads, or anything else. Idempotent:
// missing entries are skipped. Returns the names actually removed.
func clearBuildCacheDirs(ctx context.Context, workDir string) ([]string, error) {
	if err := safeCacheWorkDir(workDir); err != nil {
		return nil, err
	}
	names := []string{"node_modules", ".turbo", ".cache", ".parcel-cache", ".vite", ".eslintcache", ".pytest_cache", ".next/cache"}
	removed := []string{}
	for _, name := range names {
		target := filepath.Join(workDir, name)
		if _, err := agent.RunCommand(ctx, "test", "-e", target); err != nil {
			continue
		}
		if _, err := agent.RunCommand(ctx, "rm", "-rf", "--", target); err != nil {
			continue
		}
		removed = append(removed, name)
	}

	// Nested artifacts, after node_modules is gone so the tree is small. These
	// run find via exec args (NO shell) so workDir — which can contain a custom
	// InstallPath — can never be interpreted as a shell command.
	probe := func(label string, matchArgs ...string) bool {
		args := append([]string{workDir, "-xdev"}, matchArgs...)
		args = append(args, "-print", "-quit")
		res, err := agent.RunCommand(ctx, "find", args...)
		return err == nil && res != nil && strings.TrimSpace(res.Output) != ""
	}
	if probe("*.tsbuildinfo", "-type", "f", "-name", "*.tsbuildinfo") {
		_, _ = agent.RunCommand(ctx, "find", workDir, "-xdev", "-type", "f", "-name", "*.tsbuildinfo", "-delete")
		removed = append(removed, "*.tsbuildinfo")
	}
	if probe("__pycache__", "-type", "d", "-name", "__pycache__") {
		_, _ = agent.RunCommand(ctx, "find", workDir, "-xdev", "-type", "d", "-name", "__pycache__", "-prune", "-exec", "rm", "-rf", "{}", "+")
		removed = append(removed, "__pycache__")
	}
	return removed, nil
}
