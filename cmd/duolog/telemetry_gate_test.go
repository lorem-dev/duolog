package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/calibrate"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/store"
)

// protectedGateSetup starts a worker in a temporary home whose telemetry and
// balance roots do not exist, as before collection starts or with it off.
func protectedGateSetup(t *testing.T) (root, home string, roots []string) {
	t.Helper()
	_, root = gateSetup(t, mode.Full)
	home = os.Getenv("HOME")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	roots, err := gateProtectedRoots()
	if err != nil || len(roots) != 2 {
		t.Fatal(roots, err)
	}
	for _, r := range roots {
		if !strings.HasPrefix(r, home) {
			t.Fatalf("root %s outside the temporary home %s", r, home)
		}
	}
	return root, home, roots
}

func TestProtectedRootsInWorkerHook(t *testing.T) {
	for _, collection := range []string{"", "off"} {
		t.Run("telemetry="+collection, func(t *testing.T) {
			root, home, roots := protectedGateSetup(t)
			t.Setenv("DUOLOG_TELEMETRY", collection)
			telemetryRoot, balanceRoot := roots[0], roots[1]
			for name, call := range map[string]map[string]any{
				"read":      {"tool_name": "Read", "tool_input": map[string]string{"file_path": filepath.Join(telemetryRoot, "salt")}},
				"read new":  {"tool_name": "Read", "tool_input": map[string]string{"file_path": filepath.Join(telemetryRoot, "v1", "_account", "2026-11-05.ndjson")}},
				"write":     {"tool_name": "Write", "tool_input": map[string]string{"file_path": filepath.Join(balanceRoot, "state.json"), "content": "{}"}},
				"grep home": {"tool_name": "Grep", "tool_input": map[string]string{"path": home, "pattern": "token"}},
				// The macOS roots sit under "Application Support", so commands quote them.
				"cat":         {"tool_name": "Bash", "tool_input": map[string]string{"command": `cat "` + filepath.ToSlash(filepath.Join(telemetryRoot, "salt")) + `"`}},
				"rg ancestor": {"tool_name": "Bash", "tool_input": map[string]string{"command": `rg -uu token '` + filepath.ToSlash(filepath.Dir(balanceRoot)) + `'`}},
				"telemetry":   {"tool_name": "Bash", "tool_input": map[string]string{"command": "duolog telemetry show"}},
				"lead flag":   {"tool_name": "Bash", "tool_input": map[string]string{"command": "duolog --lead lead-123456 telemetry show"}},
				"balance":     {"tool_name": "Bash", "tool_input": map[string]string{"command": "scripts/dev/bin/duolog balance"}},
			} {
				t.Run(name, func(t *testing.T) {
					call["cwd"] = root
					data, _ := json.Marshal(call)
					out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PreToolUse")
					if err != nil || !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "closed to workers") {
						t.Fatal(out, err)
					}
				})
			}
			// No question reached the bus, and every deny names the rule.
			if bus, err := os.ReadFile(filepath.Join(root, ".duolog", "bus.ndjson")); err == nil && strings.Contains(string(bus), `"ask"`) {
				t.Fatalf("an ask reached the bus: %s", bus)
			}
			decisions, err := os.ReadFile(filepath.Join(root, ".duolog", "decisions.ndjson"))
			if err != nil || strings.Count(string(decisions), policy.RuleProtected) != 9 {
				t.Fatalf("decisions: %s %v", decisions, err)
			}
			// The worker hook only resolved the roots: it created no store and no salt.
			for _, r := range roots {
				if _, err := os.Stat(r); !os.IsNotExist(err) {
					t.Fatalf("%s: %v", r, err)
				}
				if _, err := os.Stat(filepath.Dir(r)); !os.IsNotExist(err) {
					t.Fatalf("%s: %v", filepath.Dir(r), err)
				}
			}
		})
	}
}

// TestCalibrationCellCannotWriteProtectedRoots: calibration cells and judge
// calls delegate with the same roots as every other delegation, and a cell's
// worker, which runs in a tree outside the project, cannot write into them.
func TestCalibrationCellCannotWriteProtectedRoots(t *testing.T) {
	root, _, roots := protectedGateSetup(t)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if env := cellEnv(testDeps(t), root, s, "", "lead-123456", nil); !slices.Equal(env.ProtectedRoots, roots) {
		t.Fatalf("calibration roots %q, want %q", env.ProtectedRoots, roots)
	}
	tree, err := calibration.TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tree, 0700); err != nil {
		t.Fatal(err)
	}
	telemetryRoot, balanceRoot := roots[0], roots[1]
	for name, call := range map[string]map[string]any{
		"write telemetry": {"tool_name": "Write", "tool_input": map[string]string{"file_path": filepath.Join(telemetryRoot, "v1", "_account", "2026-11-05.ndjson"), "content": "{}"}},
		"edit balance":    {"tool_name": "Edit", "tool_input": map[string]string{"file_path": filepath.Join(balanceRoot, "state.json"), "old_string": "{", "new_string": "{}"}},
		"shell write":     {"tool_name": "Bash", "tool_input": map[string]string{"command": `touch "` + filepath.ToSlash(filepath.Join(telemetryRoot, "salt")) + `"`}},
	} {
		t.Run(name, func(t *testing.T) {
			call["cwd"] = tree
			data, _ := json.Marshal(call)
			out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PreToolUse")
			if err != nil || !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "closed to workers") {
				t.Fatal(out, err)
			}
		})
	}
	for _, r := range roots {
		if _, err := os.Stat(r); !os.IsNotExist(err) {
			t.Fatalf("%s: %v", r, err)
		}
	}
}

func TestProtectedRootsKeepProjectPolicy(t *testing.T) {
	root, _, _ := protectedGateSetup(t)
	data, _ := json.Marshal(map[string]any{"tool_name": "Bash", "cwd": root, "tool_input": map[string]string{"command": "go test ./..."}})
	out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PreToolUse")
	if err != nil || out != "" {
		t.Fatal(out, err)
	}
}

// TestProtectedRootsUnresolvedFailClosed: without a home the roots cannot be
// placed, so the worker gate denies instead of letting a read through.
func TestProtectedRootsUnresolvedFailClosed(t *testing.T) {
	root, _, _ := protectedGateSetup(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("LOCALAPPDATA", "")
	data, _ := json.Marshal(map[string]any{"tool_name": "Bash", "cwd": root, "tool_input": map[string]string{"command": "go test ./..."}})
	out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PreToolUse")
	if err != nil || !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "gate failed closed") || !strings.Contains(out, "telemetry") {
		t.Fatal(out, err)
	}
}
