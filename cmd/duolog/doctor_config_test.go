package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A user config that this duolog cannot load is the first thing doctor
// reports, with its file and line, and doctor fails.
func TestDoctorReportsConfigErrorFirst(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	fakeRexFirst(t)
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "duolog", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("models: {}\nnewer_section:\n  on: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "doctor")
	if err == nil {
		t.Fatalf("doctor passed with a broken config: %q", out)
	}
	first, _, _ := strings.Cut(out, "\n")
	want := "error: config error in " + path + ", line 2: config.newer_section: unknown key; hooks and the gate cannot load their rules"
	if first != want {
		t.Fatalf("doctor first line %q, want %q", first, want)
	}
}
