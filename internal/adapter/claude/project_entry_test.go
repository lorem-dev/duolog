package claude

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// Claude keys a project by the folder it ran in, with forward slashes on
// Windows. A key in another case and with forward slashes names the same
// Windows folder, on every host.
func TestProjectEntryReadsAWindowsKeyAsClaudeWritesIt(t *testing.T) {
	projects := map[string]claudeProject{"c:/users/me/lead": {Enabled: []string{"a"}}}
	entry, found, ambiguous := projectEntry(projects, `C:\Users\Me\Lead`)
	if !found || ambiguous || !slices.Equal(entry.Enabled, []string{"a"}) {
		t.Fatalf("%+v %v %v", entry, found, ambiguous)
	}
	if _, found, _ := projectEntry(projects, `C:\Users\Me\Other`); found {
		t.Fatal("another folder's entry was taken")
	}
}

// A key spelled as root wins over another spelling of the same folder, as it
// did before other spellings were read.
func TestProjectEntryPrefersTheKeySpelledAsRoot(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	testbin.Symlink(t, root, alias)
	projects := map[string]claudeProject{root: {Enabled: []string{"a"}}, alias: {Enabled: []string{"b"}}}
	if entry, found, ambiguous := projectEntry(projects, root); !found || ambiguous || !slices.Equal(entry.Enabled, []string{"a"}) {
		t.Fatalf("%+v %v %v", entry, found, ambiguous)
	}
}

// A lead root that Claude keyed under another spelling, such as a symlink to
// it, keeps its MCP decisions; two spellings whose decisions differ enable
// nothing.
func TestPaneSettingsReadTheLeadEntryUnderAnotherSpelling(t *testing.T) {
	r := newStartRig(t)
	alias := filepath.Join(t.TempDir(), "alias")
	testbin.Symlink(t, r.lead, alias)
	r.writeProjects(map[string]any{alias: map[string]any{"enabledMcpjsonServers": []string{"a"}}})
	r.want([]string{"a"}, []string{"b"})

	other := filepath.Join(t.TempDir(), "other")
	testbin.Symlink(t, r.lead, other)
	r.writeProjects(map[string]any{
		alias: map[string]any{"enabledMcpjsonServers": []string{"a"}},
		other: map[string]any{"enabledMcpjsonServers": []string{"a"}, "disabledMcpjsonServers": []string{"a"}},
	})
	r.want(nil, []string{"a", "b"})

	r.writeProjects(map[string]any{
		alias: map[string]any{"enabledMcpjsonServers": []string{"a"}},
		other: map[string]any{"enabledMcpjsonServers": []string{"a"}},
	})
	r.want([]string{"a"}, []string{"b"})
}
