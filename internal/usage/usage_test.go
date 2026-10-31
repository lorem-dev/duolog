package usage

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func logFile(t *testing.T, data string) string {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, home)
	}
	dir := filepath.Join(t.TempDir(), ".duolog")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "usage.ndjson")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestStatsCost(t *testing.T) {
	path := logFile(t, "{\"role\":\"mechanical\",\"agent\":\"codex\",\"model\":\"gpt-6-luna\",\"input\":1000000,\"cached\":0,\"output\":0}\n")
	table, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(table.Total-0.10) > 1e-9 || len(table.Rows) != 1 || table.Rows[0].Input != 1000000 {
		t.Fatalf("%+v", table)
	}
}
func TestStatsWarnsWhenPriceIsForOldVersion(t *testing.T) {
	path := logFile(t, "{\"agent\":\"codex\",\"model\":\"gpt-7-luna\",\"input\":100}\n")
	table, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	warning := strings.Join(table.Warnings, " ")
	for _, want := range []string{"gpt-6-luna", "gpt-7-luna"} {
		if !strings.Contains(warning, want) {
			t.Fatal(table)
		}
	}
}
func TestStatsCachedInputAndOutput(t *testing.T) {
	path := logFile(t, "{\"agent\":\"codex\",\"model\":\"gpt-6-luna\",\"input\":1000000,\"cached\":500000,\"output\":1000000}\n")
	table, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(table.Total-.555) > 1e-9 {
		t.Fatal(table)
	}
}
func TestStatsMissingAndMalformed(t *testing.T) {
	path := logFile(t, "not json\n")
	if _, err := Summarize(path); err == nil {
		t.Fatal("expected malformed record error")
	}
	if _, err := Summarize(path + "missing"); err != nil {
		t.Fatal(err)
	}
}
func TestStatsUsesProjectPrices(t *testing.T) {
	path := logFile(t, "{\"agent\":\"codex\",\"model\":\"gpt-6-luna\",\"input\":1000000}\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "config.yaml"), []byte("models:\n  luna:\n    input: 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	table, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	if table.Total != 3 {
		t.Fatal(table)
	}
}

func TestStatsClaudeAliasHasUnknownActualVersion(t *testing.T) {
	path := logFile(t, `{"role":"review_final","agent":"claude","model":"opus","effort":"high","effort_capped":false,"input":1000000,"cached":0,"output":0}`+"\n")
	table, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Rows) != 1 || table.Rows[0].Model != "opus" || table.Total != 4 {
		t.Fatalf("%+v", table)
	}
	warning := strings.Join(table.Warnings, "\n")
	if !strings.Contains(warning, "prices for claude-opus-5-5") || !strings.Contains(warning, "actual version unknown") || strings.Contains(warning, "version that ran was opus") {
		t.Fatal(warning)
	}
}
