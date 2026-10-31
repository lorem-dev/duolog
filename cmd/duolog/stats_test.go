package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatsCommand(t *testing.T) {
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".duolog", "usage.ndjson"), []byte("{\"agent\":\"codex\",\"model\":\"gpt-6-luna\",\"input\":1000000}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0.100000") {
		t.Fatal(out)
	}
}
