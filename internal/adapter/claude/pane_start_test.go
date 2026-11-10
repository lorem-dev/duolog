package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/screen"
)

// startRig is a lead root and a pane folder with a .mcp.json of the servers a
// and b, under a home folder of its own.
type startRig struct {
	t                  *testing.T
	home, lead, folder string
}

func newStartRig(t *testing.T) *startRig {
	t.Helper()
	r := &startRig{t: t, home: t.TempDir(), lead: t.TempDir(), folder: t.TempDir()}
	r.write(filepath.Join(r.folder, ".mcp.json"), `{"mcpServers":{"b":{"command":"x"},"a":{"command":"y"}}}`)
	r.write(filepath.Join(r.lead, ".mcp.json"), `{"mcpServers":{"b":{"command":"x"},"a":{"command":"y"}}}`)
	return r
}

func (r *startRig) write(path, body string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// state writes Claude's user state with the project entry of the lead root.
func (r *startRig) state(entry map[string]any) {
	r.t.Helper()
	r.writeProjects(map[string]any{r.lead: entry})
}

// writeProjects writes Claude's user state with the project entries given.
func (r *startRig) writeProjects(projects map[string]any) {
	r.t.Helper()
	data, err := json.Marshal(map[string]any{"projects": projects})
	if err != nil {
		r.t.Fatal(err)
	}
	r.write(filepath.Join(r.home, ".claude.json"), string(data))
}

func (r *startRig) settings() map[string]any {
	return Pane{}.Settings(agent.PaneStart{LeadRoot: r.lead, Folder: r.folder, Environ: r.environ()})
}

// environ names the rig's home as Claude reads it on every system: HOME, and
// USERPROFILE on Windows.
func (r *startRig) environ() []string {
	return []string{"HOME=" + r.home, "USERPROFILE=" + r.home}
}

func (r *startRig) lists() (enabled, disabled []string) {
	r.t.Helper()
	s := r.settings()
	enabled, _ = s["enabledMcpjsonServers"].([]string)
	disabled, _ = s["disabledMcpjsonServers"].([]string)
	return enabled, disabled
}

func (r *startRig) want(enabled, disabled []string) {
	r.t.Helper()
	gotEnabled, gotDisabled := r.lists()
	if !slices.Equal(gotEnabled, enabled) || !slices.Equal(gotDisabled, disabled) {
		r.t.Fatalf("enabled %v disabled %v, want enabled %v disabled %v", gotEnabled, gotDisabled, enabled, disabled)
	}
}

func TestPaneSettingsEnableWhatTheLeadEnabled(t *testing.T) {
	r := newStartRig(t)
	r.state(map[string]any{"enabledMcpjsonServers": []string{"a"}})
	r.want([]string{"a"}, []string{"b"})
	if r.settings()["preferredNotifChannel"] != "notifications_disabled" {
		t.Fatal("the notification setting is gone")
	}
}

func TestPaneSettingsDisableWhatTheLeadDisabled(t *testing.T) {
	r := newStartRig(t)
	r.state(map[string]any{"enabledMcpjsonServers": []string{"a", "b"}, "disabledMcpjsonServers": []string{"b"}})
	r.want([]string{"a"}, []string{"b"})
}

func TestPaneSettingsDisableWhatTheLeadNeverDecided(t *testing.T) {
	r := newStartRig(t)
	r.want(nil, []string{"a", "b"})
	r.state(map[string]any{})
	r.want(nil, []string{"a", "b"})
}

func TestPaneSettingsEnableAllFromTheLeadsSettings(t *testing.T) {
	r := newStartRig(t)
	r.write(filepath.Join(r.home, ".claude", "settings.json"), `{"enableAllProjectMcpServers":true,"disabledMcpjsonServers":["b"]}`)
	r.want([]string{"a"}, []string{"b"})
	r.write(filepath.Join(r.home, ".claude", "settings.json"), `{"enableAllProjectMcpServers":true}`)
	r.want([]string{"a", "b"}, nil)
}

func TestPaneSettingsEnableAllInTheProjectEntry(t *testing.T) {
	r := newStartRig(t)
	r.state(map[string]any{"enableAllProjectMcpServers": true})
	r.want([]string{"a", "b"}, nil)
}

func TestPaneSettingsFailClosedOnUnreadableConfig(t *testing.T) {
	r := newStartRig(t)
	r.state(map[string]any{"enabledMcpjsonServers": []string{"a", "b"}})
	r.write(filepath.Join(r.home, ".claude.json"), `{not json`)
	r.want(nil, []string{"a", "b"})

	r.state(map[string]any{"enabledMcpjsonServers": []string{"a", "b"}})
	r.write(filepath.Join(r.home, ".claude", "settings.json"), `{not json`)
	r.want(nil, []string{"a", "b"})
}

// A settings file under a folder that cannot be entered is unreadable, not
// missing: the disable it may hold is unknown, so nothing is enabled.
func TestPaneSettingsFailClosedWhenASettingsFolderCannotBeEntered(t *testing.T) {
	r := newStartRig(t)
	r.state(map[string]any{"hasTrustDialogAccepted": true, "enabledMcpjsonServers": []string{"a"}})
	r.write(filepath.Join(r.lead, ".claude", "settings.local.json"), `{"disabledMcpjsonServers":["a"]}`)
	r.want(nil, []string{"a", "b"})
	dir := filepath.Join(r.lead, ".claude")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.Stat(filepath.Join(dir, "settings.local.json")); err == nil {
		t.Skip("the folder can be entered anyway, as for root")
	}
	r.want(nil, []string{"a", "b"})
}

func TestPaneSettingsFailClosedWithoutAHome(t *testing.T) {
	r := newStartRig(t)
	s := Pane{}.Settings(agent.PaneStart{LeadRoot: r.lead, Folder: r.folder, Environ: []string{}})
	if disabled, _ := s["disabledMcpjsonServers"].([]string); !slices.Equal(disabled, []string{"a", "b"}) {
		t.Fatalf("%v", s)
	}
	if enabled, _ := s["enabledMcpjsonServers"].([]string); len(enabled) != 0 {
		t.Fatalf("%v", s)
	}
}

func TestPaneSettingsWithoutProjectServersAddNoLists(t *testing.T) {
	r := newStartRig(t)
	if err := os.Remove(filepath.Join(r.folder, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	s := r.settings()
	if _, ok := s["enabledMcpjsonServers"]; ok {
		t.Fatalf("%v", s)
	}
	if _, ok := s["disabledMcpjsonServers"]; ok {
		t.Fatalf("%v", s)
	}
}

func TestPaneSettingsIgnoreProjectSettingsOfAnUntrustedLeadRoot(t *testing.T) {
	r := newStartRig(t)
	r.write(filepath.Join(r.lead, ".claude", "settings.json"), `{"enableAllProjectMcpServers":true}`)
	r.want(nil, []string{"a", "b"})
	r.state(map[string]any{"hasTrustDialogAccepted": true})
	r.want([]string{"a", "b"}, nil)
}

func TestPaneTrustsFolderWhenClaudeTrustsTheRootOrAParent(t *testing.T) {
	r := newStartRig(t)
	environ := r.environ()
	if (Pane{}).TrustsFolder(r.lead, environ) {
		t.Fatal("trusted with no state")
	}
	r.state(map[string]any{"hasTrustDialogAccepted": false})
	if (Pane{}).TrustsFolder(r.lead, environ) {
		t.Fatal("trusted a root whose dialog was not accepted")
	}
	r.state(map[string]any{"hasTrustDialogAccepted": true})
	if !(Pane{}).TrustsFolder(r.lead, environ) {
		t.Fatal("did not trust the accepted root")
	}
	if !(Pane{}).TrustsFolder(filepath.Join(r.lead, "sub"), environ) {
		t.Fatal("did not trust below the accepted root")
	}
	r.write(filepath.Join(r.home, ".claude.json"), `{not json`)
	if (Pane{}).TrustsFolder(r.lead, environ) {
		t.Fatal("trusted from an unreadable state")
	}
	if (Pane{}).TrustsFolder(r.lead, []string{}) {
		t.Fatal("trusted with no home")
	}
}

func TestPaneFolderTrustReadsClaudesQuestionOnly(t *testing.T) {
	trust := screen.Parse([]byte(fixtureTrust(t)), Facts.Pane)
	if label, folders, ok := (Pane{}).FolderTrust(trust); !ok || label != "Yes, I trust this folder" || len(folders) != 2 {
		t.Fatalf("%q %v %v", label, folders, ok)
	}
	if _, _, ok := (Pane{}).FolderTrust(screen.Parse([]byte(lspOffer), Facts.Pane)); ok {
		t.Fatal("a plugin offer read as a trust question")
	}
}

func fixtureTrust(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "bench", "fixtures", "startup", "claude-trust-folder.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
