package services

import "testing"

func TestSanitiseFTPSuffix(t *testing.T) {
	cases := map[string]string{
		"My File.2":    "myfile2",
		"___":          "",
		"abc":          "abc",
		"a-b/c":        "abc",
		"_lead_trail_": "lead_trail",
		"UPPER":        "upper",
		"  spaced  ":   "spaced",
		"site.com":     "sitecom",
		"":             "",
	}
	for in, want := range cases {
		if got := sanitiseFTPSuffix(in); got != want {
			t.Errorf("sanitiseFTPSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}
