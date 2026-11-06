package workerenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// pluginsBuild builds a Claude launch whose user settings enable three
// plugins besides duolog's own, and returns the enabledPlugins of its
// --settings JSON.
func pluginsBuild(t *testing.T, shape Shape, inherit bool, project string, needs ...string) map[string]bool {
	t.Helper()
	tmp := t.TempDir()
	home, root := filepath.Join(tmp, "home"), filepath.Join(tmp, "project")
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".claude", "settings.json"),
		`{"enabledPlugins":{"superpowers@market":true,"figma@market":true,"crit@market":true,"old@market":false,"duolog@duolog":true}}`)
	if project != "" {
		write(filepath.Join(root, ".claude", "settings.json"), project)
	}
	cfg := &config.Config{EnvPassthrough: []string{"HOME"}}
	cfg.Workers.InheritPlugins = inherit
	loc := Locations{Home: home, Root: root, GOOS: "linux", Environ: []string{"HOME=" + home}}
	var layers []LayerFacts
	for _, layer := range SettingsLayers("claude", loc) {
		if !layer.Managed {
			layers = append(layers, ReadLayer("claude", layer, nil, os.ReadFile, os.Stat))
		}
	}
	proc, err := Build(ProcessInput{Agent: "claude", Shape: shape, Variant: Free, Config: cfg,
		Locations: loc, Environ: loc.Environ, Layers: layers, PluginNeeds: needs})
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Enabled map[string]bool `json:"enabledPlugins"`
	}
	if proc.Settings != "" {
		if err := json.Unmarshal([]byte(proc.Settings), &settings); err != nil {
			t.Fatal(err)
		}
	}
	return settings.Enabled
}

func TestWorkerTurnsOffInheritedPluginsExceptDuolog(t *testing.T) {
	for _, shape := range []Shape{Worker, Peer} {
		got := pluginsBuild(t, shape, false, "")
		want := map[string]bool{"superpowers@market": false, "figma@market": false, "crit@market": false, "duolog@duolog": true}
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", shape, got, want)
		}
		for key, value := range want {
			if v, ok := got[key]; !ok || v != value {
				t.Fatalf("%s: got %v, want %v", shape, got, want)
			}
		}
	}
}

func TestWorkerPluginsFollowTheLastLayer(t *testing.T) {
	// The project turns one plugin off and another on; the later layer wins.
	got := pluginsBuild(t, Worker, false, `{"enabledPlugins":{"figma@market":false,"extra@market":true}}`)
	if got["figma@market"] || !got["duolog@duolog"] {
		t.Fatal(got)
	}
	if v, ok := got["extra@market"]; !ok || v {
		t.Fatal("a project plugin must be turned off", got)
	}
}

func TestInheritPluginsKeepsThePersonsPlugins(t *testing.T) {
	if got := pluginsBuild(t, Worker, true, ""); len(got) != 0 {
		t.Fatalf("inherit_plugins must leave enabledPlugins out, got %v", got)
	}
}

func TestNeededPluginStaysOn(t *testing.T) {
	// A need names the plugin with or without its marketplace.
	for _, need := range []string{"figma", "figma@market"} {
		got := pluginsBuild(t, Worker, false, "", need)
		want := map[string]bool{"superpowers@market": false, "crit@market": false, "duolog@duolog": true}
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", need, got, want)
		}
		for key, value := range want {
			if v, ok := got[key]; !ok || v != value {
				t.Fatalf("%s: got %v, want %v", need, got, want)
			}
		}
	}
}

func TestNeededPluginThatIsNotEnabledAddsNothing(t *testing.T) {
	got := pluginsBuild(t, Worker, false, "", "unknown")
	if len(got) != 4 {
		t.Fatal(got)
	}
	for key, on := range got {
		if on && key != "duolog@duolog" {
			t.Fatal("no new true", got)
		}
	}
}

func TestLeadSettingsAreNotTouchedByPluginSwitch(t *testing.T) {
	if got := pluginsBuild(t, Lead, false, ""); len(got) != 0 {
		t.Fatal(got)
	}
}
