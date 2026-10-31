package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedRouteConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
}
func TestRouteJSON(t *testing.T) {
	isolatedRouteConfig(t)
	cmd := newRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"route", "review_intermediate", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var route map[string]any
	if err := json.Unmarshal(output.Bytes(), &route); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"agent": "codex", "model": "terra", "effort": "medium", "sandbox": "read-only", "context": "project", "session": "fresh", "is_fallback": false, "self": false, "in_lead": false, "effort_capped": false} {
		if got, ok := route[key]; !ok || got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
}
func TestRouteTextAndErrors(t *testing.T) {
	isolatedRouteConfig(t)
	for _, tc := range []struct {
		args []string
		want string
		fail bool
	}{
		{[]string{"route", "plan"}, "claude", false},
		{[]string{"route", "missing"}, "missing", true},
		{[]string{"route"}, "", true},
		{[]string{"route", "plan", "extra"}, "", true},
	} {
		cmd := newRootCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if (err != nil) != tc.fail {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if err != nil {
			output.WriteString(err.Error())
		}
		if !strings.Contains(output.String(), tc.want) {
			t.Fatalf("%v: %s", tc.args, output.String())
		}
	}
}
func TestRouteManualModelFlag(t *testing.T) {
	isolatedRouteConfig(t)
	// A user layer avoids changing the shared project configuration.
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "duolog")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("roles:\n  plan:\n    use: {model: fable}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, allow := range []bool{false, true} {
		cmd := newRootCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		args := []string{"route", "plan", "--json"}
		if allow {
			args = append(args, "--allow-manual-model")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); (err == nil) != allow {
			t.Fatalf("allow=%v: %v", allow, err)
		}
	}
}
