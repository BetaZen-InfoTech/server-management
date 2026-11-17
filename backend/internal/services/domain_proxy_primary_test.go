package services

import (
	"reflect"
	"testing"
)

// TestExcludePrimaryDomain locks the fix for the duplicate "Required DNS
// records" row: a service's primary domain can carry the durable
// proxy_service_id link (so it appears in attachedDomainNames), and must be
// stripped from the ADDITIONAL-domain list the panel renders so it is never
// shown both as the primary and as an attached domain.
func TestExcludePrimaryDomain(t *testing.T) {
	cases := []struct {
		name    string
		all     []string
		primary string
		want    []string
	}{
		{
			name:    "primary present is removed",
			all:     []string{"new-qr.marinersarena.in"},
			primary: "new-qr.marinersarena.in",
			want:    []string{},
		},
		{
			name:    "case and whitespace insensitive",
			all:     []string{"  NEW-QR.MarinersArena.in "},
			primary: "new-qr.marinersarena.in",
			want:    []string{},
		},
		{
			name:    "genuine extra domains survive",
			all:     []string{"new-qr.marinersarena.in", "qr.example.com"},
			primary: "new-qr.marinersarena.in",
			want:    []string{"qr.example.com"},
		},
		{
			name:    "empty primary is a no-op",
			all:     []string{"a.example.com", "b.example.com"},
			primary: "",
			want:    []string{"a.example.com", "b.example.com"},
		},
		{
			name:    "no attached domains",
			all:     []string{},
			primary: "new-qr.marinersarena.in",
			want:    []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := excludePrimaryDomain(tc.all, tc.primary)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("excludePrimaryDomain(%v, %q) = %v, want %v", tc.all, tc.primary, got, tc.want)
			}
		})
	}
}
