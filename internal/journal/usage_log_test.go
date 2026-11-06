package journal

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/usage"
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
	table, err := SummarizeUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(table.Total-0.10) > 1e-9 || len(table.Rows) != 1 || table.Rows[0].Input != 1000000 {
		t.Fatalf("%+v", table)
	}
}
func TestStatsWarnsWhenPriceIsForOldVersion(t *testing.T) {
	path := logFile(t, "{\"agent\":\"codex\",\"model\":\"gpt-7-luna\",\"input\":100}\n")
	table, err := SummarizeUsage(path)
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
	table, err := SummarizeUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(table.Total-.555) > 1e-9 {
		t.Fatal(table)
	}
}
func TestStatsMissingAndMalformed(t *testing.T) {
	path := logFile(t, "not json\n")
	if _, err := SummarizeUsage(path); err == nil {
		t.Fatal("expected malformed record error")
	}
	if _, err := SummarizeUsage(path + "missing"); err != nil {
		t.Fatal(err)
	}
}
func TestStatsUsesProjectPrices(t *testing.T) {
	path := logFile(t, "{\"agent\":\"codex\",\"model\":\"gpt-6-luna\",\"input\":1000000}\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "config.yaml"), []byte("models:\n  luna:\n    input: 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	table, err := SummarizeUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if table.Total != 3 {
		t.Fatal(table)
	}
}

func TestStatsClaudeAliasHasUnknownActualVersion(t *testing.T) {
	path := logFile(t, `{"role":"review_final","agent":"claude","model":"opus","effort":"high","effort_capped":false,"input":1000000,"cached":0,"output":0}`+"\n")
	table, err := SummarizeUsage(path)
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

// A recorded Claude result line, as delegate writes it to usage.ndjson, costs
// what Claude reported for it: total_cost_usd 0.00128465 for Haiku with 4
// input, 28035 cache reads, 4792 one-hour cache writes and 91 output tokens.
// The same tokens as plain input, a record from before cache writes were
// kept, cost less and as before.
func TestStatsPricesRecordedClaudeCacheWrites(t *testing.T) {
	recorded := `{"role":"mechanical","agent":"claude","model":"haiku","effort":"low","effort_capped":false,"input":32831,"cached":28035,"cache_write":4792,"cache_write_1h":4792,"output":91}`
	old := `{"role":"mechanical","agent":"claude","model":"haiku","effort":"low","effort_capped":false,"input":32831,"cached":28035,"output":91}`
	summarize := func(line string) usage.Table {
		t.Helper()
		table, err := SummarizeUsage(logFile(t, line+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		return table
	}
	withWrites, without := summarize(recorded), summarize(old)
	if math.Abs(withWrites.Total-0.00128465) > 1e-9 {
		t.Fatalf("recorded result costs %v, Claude reported 0.00128465", withWrites.Total)
	}
	// 4796 plain input at 0.10, 28035 reads at 0.01 and 91 output at 0.50 per 1M.
	if math.Abs(without.Total-0.00080545) > 1e-9 || without.Total >= withWrites.Total {
		t.Fatalf("old record costs %v, want 0.00080545 and less than %v", without.Total, withWrites.Total)
	}
	row := withWrites.Rows[0]
	if row.Input != 32831 || row.Cached != 28035 || row.Write != 4792 || row.WriteHour != 4792 || row.Output != 91 {
		t.Fatalf("%+v", row)
	}
	if without.Rows[0].Write != 0 {
		t.Fatalf("old record has cache writes: %+v", without.Rows[0])
	}
}

func TestStatsRejectsCacheWritesOutsideInput(t *testing.T) {
	for _, line := range []string{
		`{"agent":"claude","model":"opus","input":100,"cached":60,"cache_write":50}`,
		`{"agent":"claude","model":"opus","input":100,"cache_write":10,"cache_write_1h":20}`,
		`{"agent":"claude","model":"opus","input":100,"cache_write":-1}`,
	} {
		if _, err := SummarizeUsage(logFile(t, line+"\n")); err == nil {
			t.Fatalf("%s: expected invalid token counts", line)
		}
	}
}
