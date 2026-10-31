package gate

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestImportedDenyWithShortRoot(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	long, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	short := testbin.ShortPath(t, root)
	for _, pattern := range []string{
		filepath.ToSlash(filepath.Join(long, "restricted", "**")),
		"/" + filepath.ToSlash(filepath.Join(long, "restricted", "**")),
		"/" + filepath.ToSlash(filepath.Join(short, "restricted", "**")),
	} {
		data, err := json.Marshal(map[string]any{"permissions": map[string]any{"deny": []string{"Read(" + pattern + ")"}}})
		if err != nil {
			t.Fatal(err)
		}
		writeSetting(t, filepath.Join(short, ".claude", "settings.json"), string(data))
		lead, err := ImportLead(short, "claude")
		if err != nil {
			t.Fatal(err)
		}
		check(t, p, lead, mode.Full, Action{Kind: "read", Target: "restricted/new.txt", Role: "implement"}, Deny)
		check(t, p, lead, mode.Full, Action{Kind: "read", Target: "public/new.txt", Role: "implement"}, Allow)
	}
}
