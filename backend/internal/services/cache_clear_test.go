package services

import "testing"

func TestSafeCacheWorkDir(t *testing.T) {
	cases := []struct {
		dir string
		ok  bool
	}{
		{"/home/u/apps/x", true},
		{"/home/u/projects/slug/web", true},
		{"/opt/apps/x", true},
		{"/srv/app/current", true},
		{"/var/www/site/current", true},
		{"", false},
		{"/", false},
		{"/home", false},
		{"/home/u", false},
		{"/home/u/apps", false},
		{"/opt", false},
		{"/opt/app", false},
		{"/var/log/x", false},
		{"/usr/local/app/x", false},
		{"relative/x", false},
		{"/home/u/../../etc", false},
		{"/home/u/apps/x/", false},
		{"/etc/a/b/c", false},
	}
	for _, tc := range cases {
		err := safeCacheWorkDir(tc.dir)
		if (err == nil) != tc.ok {
			t.Errorf("safeCacheWorkDir(%q) err=%v, want ok=%v", tc.dir, err, tc.ok)
		}
	}
}
