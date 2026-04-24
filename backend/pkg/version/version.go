// Package version is the single source of truth for the product name and
// release number. The frontend fetches these over /api/v1/version and
// renders them in the WHM top bar so every surface — logs, health checks,
// UI, API responses — reads from the same constants.
//
// Bumping a release:
//   - patch fix        → bump Patch
//   - new feature      → bump Minor, reset Patch
//   - breaking change  → bump Major, reset Minor + Patch
//
// Anything more sophisticated (build SHA, channel) can be set via ldflags
// later without touching call sites.
package version

import "fmt"

const (
	// Name is the product name shown next to the version in the UI.
	Name = "Betazen Server Panel"

	// Major, Minor, Patch make up the semantic version. Update here; the
	// API response and frontend header pick it up automatically.
	//
	// 3.0.1 (2026-04-24) — OTP magic-link cross-browser handoff: the
	// originating browser polls for approval and auto-completes when
	// the emailed URL is clicked from another browser (e.g. your
	// mailbox open in Browser B while you're signing in from
	// Browser A). The clicking browser never gets a session; the
	// binding-cookie gate still blocks forwarded-link takeovers.
	// Also removes the legacy binding_hash="" carveout now that any
	// in-flight OTPs from 3.0.0 have expired.
	Major = 3
	Minor = 0
	Patch = 1
)

// Number returns the semantic version as "MAJOR.MINOR.PATCH". The
// Patch component auto-increments via .github/workflows/bump-version.yml
// on every code-touching push to main.
func Number() string {
	return fmt.Sprintf("%d.%d.%d", Major, Minor, Patch)
}

// Info is the JSON shape returned by GET /api/v1/version.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Patch   int    `json:"patch"`
}

// Get returns the current version info. Cheap — no allocation-heavy work.
func Get() Info {
	return Info{
		Name:    Name,
		Version: Number(),
		Major:   Major,
		Minor:   Minor,
		Patch:   Patch,
	}
}
