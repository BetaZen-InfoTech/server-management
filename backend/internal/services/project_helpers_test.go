package services

import "testing"

// TestResolveServiceAppType locks in the framework → app_type mapping the
// deploy flow relies on to pick the right interpreter bin dir. Without
// this mapping, a Go backend service (role=backend, framework=go-gin)
// would fall back to roleToAppType("backend") == "node" and look up
// /usr/local/n/versions/node/<ver>/bin instead of /opt/go/<ver>/bin.
func TestResolveServiceAppType(t *testing.T) {
	cases := []struct {
		name      string
		framework string
		role      string
		want      string
	}{
		{"node preset beats role", "nextjs", "backend", "node"},
		{"go preset flips backend→go", "go-gin", "backend", "go"},
		{"python preset flips backend→python", "python-flask", "backend", "python"},
		{"ruby preset flips backend→ruby", "ruby-sinatra", "backend", "ruby"},
		{"static preset keeps static", "react-vite", "frontend", "static"},
		{"unknown framework falls back to role", "custom-thing", "backend", "node"},
		{"no framework on backend falls back to node", "", "backend", "node"},
		{"no framework on static stays static", "", "static", "static"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveServiceAppType(tc.framework, tc.role)
			if got != tc.want {
				t.Errorf("resolveServiceAppType(%q, %q) = %q, want %q",
					tc.framework, tc.role, got, tc.want)
			}
		})
	}
}

// TestResolveRuntimeBinDir_GoVersionSwitch verifies the runtime-bin-dir
// resolver produces the versioned /opt/go/<ver>/bin path that the
// deploy flow prepends to PATH — the whole point of letting operators
// pick a runtime version in the UI.
func TestResolveRuntimeBinDir_GoVersionSwitch(t *testing.T) {
	cases := []struct {
		appType string
		version string
		want    string
	}{
		{"go", "1.23.0", "/opt/go/1.23.0/bin"},
		{"go", "go-1.22.5", "/opt/go/1.22.5/bin"},
		{"node", "20.11.0", "/usr/local/n/versions/node/20.11.0/bin"},
		{"ruby", "3.3.0", "/opt/ruby/3.3.0/bin"},
		{"go", "", ""},        // empty version → system default, no pin
		{"static", "1.0", ""}, // static has no bin dir
	}
	for _, tc := range cases {
		got := resolveRuntimeBinDir(tc.appType, tc.version)
		if got != tc.want {
			t.Errorf("resolveRuntimeBinDir(%q, %q) = %q, want %q",
				tc.appType, tc.version, got, tc.want)
		}
	}
}
