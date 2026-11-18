package services

import (
	"strings"
	"testing"
)

// diagnoseStartCrash maps a crash-loop journal tail to an actionable hint when
// a backend service's health check fails because the process died before
// binding its port. These cases pin the patterns that matter most in the
// field — a DB-unreachable crash right after a server migration being the
// headline one (the app prints its env banner, then exits).
func TestDiagnoseStartCrash(t *testing.T) {
	cases := []struct {
		name    string
		journal string
		want    string // substring the hint must contain; "" means expect no hint
	}{
		{
			name:    "mongo server selection (migration: old host / not reachable)",
			journal: "[env] v2.6.7 APP_ENV=prod MONGODB=mongodb://***\nMongooseServerSelectionError: connect ECONNREFUSED 10.0.0.5:27017\n    at ...",
			want:    "MongoDB",
		},
		{
			name:    "mongo network error",
			journal: "MongoNetworkError: failed to connect to server [old-host:27017] on first connect",
			want:    "MongoDB",
		},
		{
			name:    "mongo auth failed",
			journal: "MongoServerError: Authentication failed.\n    at ...",
			want:    "credentials",
		},
		{
			name:    "postgres refused",
			journal: "SequelizeConnectionRefusedError: connect ECONNREFUSED 127.0.0.1:5432",
			want:    "database/cache",
		},
		{
			name:    "generic econnrefused (unknown backing service)",
			journal: "Error: connect ECONNREFUSED 203.0.113.9:8080\n    at TCPConnectWrap",
			want:    "backing service",
		},
		{
			name:    "missing required env var",
			journal: "Error: Config validation error: \"JWT_SECRET\" is required for process.env\n    at validateEnv",
			want:    "environment variable",
		},
		{
			name:    "already-in-use still wins over DB patterns",
			journal: "Error: listen EADDRINUSE: address already in use :::4151",
			want:    "already listening",
		},
		{
			name:    "cannot find module still detected",
			journal: "Error: Cannot find module 'express'",
			want:    "node_modules",
		},
		{
			name:    "unhandled rejection fallback",
			journal: "UnhandledPromiseRejectionWarning: something went wrong at boot",
			want:    "unhandled error",
		},
		{
			name:    "unrecognised -> no hint",
			journal: "Started service. Ready.",
			want:    "",
		},
		{
			name:    "empty -> no hint",
			journal: "",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diagnoseStartCrash(tc.journal)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("expected no hint, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("hint for %q should contain %q; got %q", tc.name, tc.want, got)
			}
		})
	}
}
