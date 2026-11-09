package lead

import (
	"strings"
	"testing"
)

func TestSessionLabel(t *testing.T) {
	cases := []struct {
		name, kind, role, lead, id, want string
	}{
		{"worker with name", "worker", "implement", "Fix the parser", "abcdef123456", "duolog-worker-implement (lead: Fix the parser)"},
		{"peer with name", "peer", "review", "Docs", "abcdef123456", "duolog-peer-review (lead: Docs)"},
		{"no name, long id", "worker", "docs", "", "abcdef123456", "duolog-worker-docs (lead: abcdef)"},
		{"no name, short id", "worker", "docs", "", "abc", "duolog-worker-docs (lead: abc)"},
		{"blank name", "peer", "docs", " \t\n ", "abcdef123456", "duolog-peer-docs (lead: abcdef)"},
		{"lines and controls fold to spaces", "worker", "docs", "a\nb\x1b[0m\tc  d", "abcdef", "duolog-worker-docs (lead: a b [0m c d)"},
		{"any text stays", "worker", "docs", "fix (lead: x) \"q\" $HOME `id` ;|&", "abcdef", "duolog-worker-docs (lead: fix (lead: x) \"q\" $HOME `id` ;|&)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SessionLabel(tc.kind, tc.role, tc.lead, tc.id); got != tc.want {
				t.Fatalf("SessionLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionLabelBoundsALongName(t *testing.T) {
	got := SessionLabel("worker", "docs", strings.Repeat("x", 500), "abcdef")
	if len(got) > 200 || !strings.HasSuffix(got, ")") || !strings.HasPrefix(got, "duolog-worker-docs (lead: x") {
		t.Fatalf("label = %q", got)
	}
}

func TestSessionLabelHasNoPath(t *testing.T) {
	if got := SessionLabel("peer", "docs", "", "abcdef"); strings.ContainsAny(got, "/\\") {
		t.Fatalf("label holds a path: %q", got)
	}
}
