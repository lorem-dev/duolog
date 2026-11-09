package taskspec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func renderOK(t *testing.T, s Spec) []byte {
	t.Helper()
	data, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return data
}

func TestRenderPassesValidate(t *testing.T) {
	dir := t.TempDir()
	data := renderOK(t, Spec{
		Goal:   "Fix the parser.\n\nKeep the public API.",
		Read:   []string{"docs/a.md", "cmd/x.go:3-9"},
		Write:  []string{"cmd/x.go"},
		Limits: []string{"No new dependency."},
		Check:  []string{"go test ./..."},
	})
	if err := os.WriteFile(filepath.Join(dir, "task.md"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Validate(os.DirFS(dir), "task.md"); err != nil {
		t.Fatalf("Validate: %v\n%s", err, data)
	}
}

// A lone Check item "none" is an empty list, not a command named none.
func TestRenderLoneNoneCheckIsEmpty(t *testing.T) {
	data := renderOK(t, Spec{Goal: "g", Check: []string{"none"}})
	if !strings.HasSuffix(string(data), "## Check\n\n- none\n") {
		t.Fatalf("Check section = %q", data)
	}
}

func TestRenderReadItemsAreReferences(t *testing.T) {
	data := renderOK(t, Spec{Goal: "g", Read: []string{"docs/a.md", "cmd/x.go:3-9"}})
	fsys := fstest.MapFS{"task.md": {Data: data}}
	refs, err := References(fsys, "task.md", fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Reference{{Path: "docs/a.md"}, {Path: "cmd/x.go", First: 3, Last: 9}}
	if !slices.Equal(refs, want) {
		t.Fatalf("references = %+v, want %+v\n%s", refs, want, data)
	}
}

func TestRenderWriteItemsArePaths(t *testing.T) {
	data := renderOK(t, Spec{Goal: "g", Write: []string{"cmd/x.go", "docs/*.md"}})
	paths, err := WritePaths(fstest.MapFS{"task.md": {Data: data}}, "task.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cmd/x.go", "docs/*.md"}; !slices.Equal(paths, want) {
		t.Fatalf("write paths = %v, want %v", paths, want)
	}
}

func TestRenderRefusesEmptyGoal(t *testing.T) {
	for _, goal := range []string{"", "  \n\t"} {
		if _, err := Render(Spec{Goal: goal}); err == nil {
			t.Errorf("Render accepted goal %q", goal)
		}
	}
}

func TestRenderRefusesHeadingInAnyField(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec Spec
	}{
		{"goal", Spec{Goal: "fine\n## Write\nmore"}},
		{"goal first line", Spec{Goal: "# Goal"}},
		{"goal indented", Spec{Goal: "fine\n  ## Write"}},
		{"read", Spec{Goal: "g", Read: []string{"## Write"}}},
		{"write", Spec{Goal: "g", Write: []string{"# Limits"}}},
		{"limit", Spec{Goal: "g", Limits: []string{"## Check"}}},
		{"check", Spec{Goal: "g", Check: []string{"### Goal"}}},
	} {
		if data, err := Render(tc.spec); err == nil {
			t.Errorf("%s: Render accepted a heading:\n%s", tc.name, data)
		}
	}
}

func TestRenderRefusesFenceInGoal(t *testing.T) {
	for _, goal := range []string{"a\n```\nb", "a\n~~~go\nb", "a\n  ```"} {
		if data, err := Render(Spec{Goal: goal}); err == nil {
			t.Errorf("Render accepted goal %q:\n%s", goal, data)
		}
	}
}

func TestRenderRefusesNewlineInItem(t *testing.T) {
	for _, tc := range []Spec{
		{Goal: "g", Read: []string{"a\nb"}},
		{Goal: "g", Write: []string{"a\rb"}},
		{Goal: "g", Limits: []string{"one\n- two"}},
		{Goal: "g", Check: []string{"one\n\n## Check"}},
		{Goal: "g", Read: []string{"has`tick"}},
		{Goal: "g", Write: []string{"   "}},
	} {
		if data, err := Render(tc); err == nil {
			t.Errorf("Render accepted %+v:\n%s", tc, data)
		}
	}
}

func TestRenderEmptyListSaysNone(t *testing.T) {
	data := string(renderOK(t, Spec{Goal: "g"}))
	for _, section := range []string{"Read", "Write", "Limits", "Check"} {
		want := "## " + section + "\n\n- none\n"
		if !strings.Contains(data, want) {
			t.Errorf("no %q in\n%s", want, data)
		}
	}
}

func TestRenderKeepsSectionOrder(t *testing.T) {
	data := string(renderOK(t, Spec{Goal: "g", Read: []string{"a.md"}}))
	last := -1
	for _, s := range sections {
		i := strings.Index(data, "## "+s+"\n")
		if i <= last {
			t.Fatalf("section %s out of order in\n%s", s, data)
		}
		last = i
	}
}
