package agentplugin

import "testing"

// TestHasMarketplace distinguishes names from sources and longer names in
// both agent CLI list formats.
func TestHasMarketplace(t *testing.T) {
	for _, tc := range []struct {
		list string
		want bool
	}{
		{"", false},
		{"duolog\n  Source: lorem-dev/duolog", true},
		{"  - duolog\n", true},
		{"  \u276f duolog\n", true},
		{"Name: duolog\n", true},
		{"duolog-extra\n  Source: lorem-dev/duolog", false},
		{`[{"name":"duolog","source":"github"}]`, true},
		{`{"marketplaces":[{"name":"duolog"}]}`, true},
		{`{"duolog":{"source":"github"}}`, true},
		{`[{"name":"other","description":"duolog"}]`, false},
		{`[{"name":"duolog-extra"}]`, false},
	} {
		if got := HasMarketplace([]byte(tc.list), "duolog"); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.list, got, tc.want)
		}
	}
}
