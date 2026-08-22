package services

import (
	"testing"
	"time"
)

// TestDNSCloudflareSyncHook proves the fire-and-forget hook plumbing: a wired
// hook receives the domain, and an unwired service is a safe no-op.
func TestDNSCloudflareSyncHook(t *testing.T) {
	s := &DNSService{}
	got := make(chan string, 1)
	s.SetCloudflareSyncHook(func(domain string) { got <- domain })
	s.fireCloudflareSync("example.com")
	select {
	case d := <-got:
		if d != "example.com" {
			t.Fatalf("hook received %q; want example.com", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cloudflare sync hook did not fire")
	}

	// No hook wired must not panic.
	(&DNSService{}).fireCloudflareSync("nohook.com")
}
