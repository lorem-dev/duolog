package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

var updateLoaded = flag.Bool("update", false, "rewrite the golden files in testdata/loaded")

// loadedFixtures is the list of config fixtures under testdata/fixtures; each
// is loaded as the user config. "empty" is a project with no config file.
var loadedFixtures = []string{"empty", "paid-runs", "coauthors", "worker-modes", "agent-sync", "routing"}

// TestFixturesLoadedGolden pins what Load returns for every fixture, so the
// move of agent-keyed config to maps changes none of it.
func TestFixturesLoadedGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(loadedFixtures)-1 {
		t.Fatalf("fixture files %v, test list %v", files, loadedFixtures)
	}
	for _, name := range loadedFixtures {
		t.Run(name, func(t *testing.T) {
			root := isolatedRoot(t)
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			if name != "empty" {
				data, err := os.ReadFile(filepath.Join("testdata", "fixtures", name+".yaml"))
				if err != nil {
					t.Fatal(err)
				}
				writeConfig(t, userConfigPath(t), string(data))
			}
			cfg, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			// The default env passthrough names more variables on Windows; the
			// golden files hold the list of the other systems.
			if slices.Equal(cfg.EnvPassthrough, config.DefaultEnvPassthrough(runtime.GOOS)) {
				cfg.EnvPassthrough = config.DefaultEnvPassthrough("linux")
			}
			got, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = bytes.ReplaceAll(got, []byte(filepath.ToSlash(home)), []byte("<home>"))
			got = bytes.ReplaceAll(got, []byte(strings.ReplaceAll(home, `\`, `\\`)), []byte("<home>"))
			if runtime.GOOS == "windows" {
				// A Windows path such as an expanded writable root comes out with
				// escaped backslashes; no golden file holds one.
				got = bytes.ReplaceAll(got, []byte(`\\`), []byte("/"))
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", "loaded", name+".json")
			if *updateLoaded {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("loaded config differs from %s at %s; run with -update only if the change is meant", path, firstDiff(got, want))
			}
		})
	}
}

// firstDiff names the first line where got and want differ.
func firstDiff(got, want []byte) string {
	g, w := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := range max(len(g), len(w)) {
		var a, b string
		if i < len(g) {
			a = g[i]
		}
		if i < len(w) {
			b = w[i]
		}
		if a != b {
			return fmt.Sprintf("line %d: got %q, want %q", i+1, a, b)
		}
	}
	return "the end"
}
