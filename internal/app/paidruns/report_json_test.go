package paidruns

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// TestReportJSONNamesAreSnakeCase is review finding M2: quota --json prints
// the assessment with snake_case keys, like the rest of the report.
func TestReportJSONNamesAreSnakeCase(t *testing.T) {
	r := Report{Assessment: paidrules.Assessment{Agent: "claude", Shape: workerenv.Worker, Variant: workerenv.Free,
		Verdict: paidrules.Paid, Account: "workspace", UsableShare: 0.5,
		Signals:        []workerenv.Signal{{Name: "auth:unread", Layer: "auth status"}},
		AccountReasons: []string{"org"}}}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Assessment map[string]json.RawMessage `json:"assessment"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for key := range doc.Assessment {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	want := []string{"account", "account_reasons", "agent", "shape", "signals", "usable_share", "variant", "verdict"}
	if !slices.Equal(keys, want) {
		t.Fatalf("assessment keys %v, want %v", keys, want)
	}
}

// TestReportAccountLinesOnce is review finding M1: the total sessions and
// the ignored config keys belong to the account and the config, so they are
// not part of an agent's lines; AccountLines says them once.
func TestReportAccountLinesOnce(t *testing.T) {
	r := Report{Assessment: paidrules.Assessment{Agent: "codex", Shape: workerenv.Worker, Verdict: paidrules.Subscription, Account: "personal"},
		TotalLive: 2, TotalLimit: 10,
		Notices: []config.ConfigNotice{{Key: "paid_runs.codex.allow", Reason: "a shared config may only make paid runs stricter"}}}
	agent := strings.Join(r.Lines(), "\n")
	if strings.Contains(agent, "in all") || strings.Contains(agent, "ignored config") {
		t.Fatalf("agent lines hold account lines:\n%s", agent)
	}
	got := r.AccountLines()
	want := []string{"sessions 2 of 10 in all",
		"ignored config: paid_runs.codex.allow (a shared config may only make paid runs stricter)"}
	if !slices.Equal(got, want) {
		t.Fatalf("account lines %q, want %q", got, want)
	}
}
