package services

import "testing"

// TestIsSafeMaildirPath pins the exact path shapes DeleteMailbox is
// allowed to feed to `rm -rf`. Pre-3.1.42 a corrupted Mongo row whose
// email field was malformed (blank, missing @-part) made
// getMaildirPath return paths like `/var/vmail/` or `/home/x/mail//`
// and `rm -rf` would happily nuke entire tenant mail trees.
//
// The two ALLOWED shapes are EXACTLY:
//
//	/var/vmail/<domain>/<localpart>      → 5 components
//	/home/<user>/mail/<domain>/<localpart> → 6 components
//
// Anything else MUST fail the guard.
func TestIsSafeMaildirPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		// Happy-path shapes — both legitimate maildir layouts.
		{"vmail variant", "/var/vmail/example.com/alice", true},
		{"home variant", "/home/alice/mail/example.com/alice", true},
		{"home variant with subdomain", "/home/alice/mail/api.example.com/info", true},

		// Empty / nil-ish paths — getMaildirPath returns "" when the
		// email's @-part is missing.
		{"empty string rejected", "", false},

		// Path traversal — a `..` anywhere in the path is an immediate fail.
		{"traversal rejected", "/var/vmail/../etc", false},
		{"traversal mid-path rejected", "/home/alice/mail/../etc/passwd", false},

		// Blank segments — `/var/vmail//foo` would become `/var/vmail/foo`
		// after rm's path resolution and could match a wrong directory.
		{"double-slash in vmail rejected", "/var/vmail//alice", false},
		{"double-slash in home rejected", "/home/alice//mail/example.com/alice", false},
		{"trailing slash rejected", "/var/vmail/example.com/alice/", false},

		// Wrong prefix — getMaildirPath should never return these,
		// but a future bug that does would otherwise nuke the
		// arbitrary path.
		{"unrelated path rejected", "/etc/passwd", false},
		{"root rejected", "/", false},
		{"home root rejected", "/home", false},
		{"vmail root rejected", "/var/vmail", false},
		{"vmail-with-trailing rejected", "/var/vmail/", false},

		// Right prefix but wrong depth — would nuke /home/<user>/mail/
		// (every domain's mail) instead of one mailbox.
		{"vmail too short rejected", "/var/vmail/example.com", false},
		{"home too short rejected", "/home/alice/mail/example.com", false},

		// Right prefix but too many segments — would point inside a
		// real mailbox and rm a sub-tree that the caller didn't intend.
		{"vmail too long rejected", "/var/vmail/example.com/alice/cur", false},
		{"home too long rejected", "/home/alice/mail/example.com/alice/cur/messages", false},

		// home variant with the wrong third segment — would nuke
		// /home/<user>/Documents/<domain>/<alice> if a buggy path
		// builder stamped the wrong literal there.
		{"home wrong third segment rejected", "/home/alice/Documents/example.com/alice", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSafeMaildirPath(tc.path)
			if got != tc.want {
				t.Errorf("isSafeMaildirPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
