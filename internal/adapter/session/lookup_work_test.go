package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestLookupWorkSeparatesProjects(t *testing.T) {
	f := submodule.New(t)
	r, err := Open(filepath.Join(f.Superproject, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{
		{ID: "super", WorkRoot: f.Superproject}, {ID: "sub", WorkRoot: f.Submodule},
		{ID: "linked", WorkRoot: f.Linked}, {ID: "legacy"},
	} {
		e.Lead, e.Agent, e.Role, e.Thread = "lead", "fake", "implement", "topic"
		if err := r.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct{ root, id string }{
		{f.Superproject, "legacy"}, {f.Submodule, "sub"}, {f.Linked, "linked"}, {f.Unrelated, ""},
	} {
		entry, found, err := r.LookupWork("lead", "fake", "implement", "topic", tt.root)
		if err != nil {
			t.Fatal(err)
		}
		if found != (tt.id != "") || found && entry.ID != tt.id {
			t.Fatalf("%s: %+v %v", tt.root, entry, found)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(f.Linked, alias); err == nil {
		e, found, err := r.LookupWork("lead", "fake", "implement", "topic", alias)
		if err != nil || !found || e.ID != "linked" {
			t.Fatalf("alias: %+v %v %v", e, found, err)
		}
	}
	legacy, err := Open(filepath.Join(f.Submodule, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Record(Entry{ID: "old", Lead: "lead", Agent: "fake", Role: "implement", Thread: "topic"}); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{f.Submodule, f.Linked, f.Superproject} {
		_, found, err := legacy.LookupWork("lead", "fake", "implement", "topic", root)
		if err != nil || found != (root == f.Submodule) {
			t.Fatalf("legacy %s: %v %v", root, found, err)
		}
	}
}
