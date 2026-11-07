package caps

import "testing"

func TestDiffIsDirectional(t *testing.T) {
	a := Inventory{Agent: "claude", Items: []Item{{Kind: "skill", Name: "x"}, {Kind: "mcp", Name: "y"}}}
	b := Inventory{Agent: "codex", Items: []Item{{Kind: "mcp", Name: "y"}, {Kind: "plugin", Name: "z"}}}
	if got := Diff(a, b); len(got) != 1 || got[0].Name != "x" {
		t.Fatal(got)
	}
	if got := Diff(b, a); len(got) != 1 || got[0].Name != "z" {
		t.Fatal(got)
	}
}

func TestDiffSkipsUnknownKinds(t *testing.T) {
	a := Inventory{Agent: "codex", Items: []Item{{Kind: KindMCP, Name: "x"}, {Kind: KindSkill, Name: "s"}}}
	b := Inventory{Agent: "claude", UnknownKinds: []string{KindMCP}}
	d := Diff(a, b)
	if len(d) != 1 || d[0].Name != "s" {
		t.Fatal(d)
	}
}
