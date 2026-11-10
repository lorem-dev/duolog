package quota

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
)

// Kills hiding a weekly-only read or omitting the probe that supplied its numbers.
func TestPrintWeeklyProbe(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	s := &account.Snapshot{Source: "rate_limit_event (haiku probe)", Plan: "max", Windows: []account.Window{{Kind: "weekly_all", UsedPercent: 9, ResetsAt: now.Add(time.Hour)}}}
	results := map[string]Result{"claude": {Snapshot: s}, "codex": {Unavailable: "not read"}}
	var out bytes.Buffer
	if err := Print(&out, results, now); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "weekly") || !strings.Contains(text, "9%") || !strings.Contains(text, s.Source) || strings.Contains(text, "5h") {
		t.Fatalf("output %s", text)
	}
}
