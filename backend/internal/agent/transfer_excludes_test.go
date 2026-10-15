package agent

import "testing"

// TestTransferExcludesDropIncrementalCaches locks the invariant that the
// per-user file transfer excludes TypeScript/monorepo *incremental build
// caches* whenever it also excludes the emitted build output.
//
// Regression guard for the "0/84 services up after migration" incident: the
// list excluded `dist`/`build` (emitted output) but kept `*.tsbuildinfo`. On
// the destination, `tsc` read the stale cache, decided everything was already
// emitted, and produced no `dist/` — so every `@scope/pkg` resolved to a
// non-existent `dist/index.d.ts` and the whole workspace failed to build with
// TS2307. Shipping an incremental cache without its output is strictly worse
// than shipping neither.
func TestTransferExcludesDropIncrementalCaches(t *testing.T) {
	set := make(map[string]bool, len(transferTarExcludes))
	for _, e := range transferTarExcludes {
		set[e] = true
	}

	// If we strip emitted output, we MUST strip the incremental cache too.
	stripsOutput := set["dist"] || set["build"]
	if stripsOutput && !set["*.tsbuildinfo"] {
		t.Fatalf("transferTarExcludes strips build output (dist/build) but keeps *.tsbuildinfo — "+
			"a stale incremental cache will suppress tsc emit on the destination; excludes=%v", transferTarExcludes)
	}

	for _, want := range []string{"*.tsbuildinfo", ".turbo"} {
		if !set[want] {
			t.Errorf("transferTarExcludes missing %q (incremental build cache that must not ride a transfer)", want)
		}
	}
}
