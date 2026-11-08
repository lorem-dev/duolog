package quota

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/record"
)

type clearedMarks struct{ agents []string }

func (m *clearedMarks) ClearUnavailable(agent string) error {
	m.agents = append(m.agents, agent)
	return nil
}

// TestCollectStatesAnEarlierRead: a command whose read fails for a passing
// reason reports on the last good read, says that it does, and treats it as
// no new observation: telemetry gets nothing and no unavailable mark clears.
func TestCollectStatesAnEarlierRead(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	gate, err := paidrunstest.Gate(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	gate.Cache = paidrunstest.KeepingCache(paidrunstest.Subscription, dir)
	marks := &clearedMarks{}
	observed := 0
	request := Request{Gate: gate, Config: cfg, Home: dir, Root: dir, GOOS: "linux", Environ: []string{"HOME=" + dir},
		Unavailable: map[string]bool{"claude": true}, Marks: marks,
		Lead:    func(string) (launch.ProcessInput, bool) { return launch.ProcessInput{}, false },
		Observe: func(record.QuotaObservation) { observed++ }}

	if _, read := Collect(context.Background(), request); read != 2 || observed != 2 || len(marks.agents) != 1 {
		t.Fatalf("good reads: read %d, observed %d, cleared %v", read, observed, marks.agents)
	}

	observed, marks.agents = 0, nil
	gate.Cache = paidrunstest.KeepingCache(func(context.Context, launch.Process) (account.Snapshot, error) {
		return account.Snapshot{}, account.TransientError("usage read failed")
	}, dir)
	results, read := Collect(context.Background(), request)
	if read != 2 || observed != 0 || len(marks.agents) != 0 {
		t.Fatalf("earlier reads: read %d, observed %d, cleared %v", read, observed, marks.agents)
	}
	peer := results["claude"].Reports["peer"]
	if peer.Assessment.Verdict != paidrules.Subscription || peer.ReusedReadSeconds < 1 || peer.ReadError != "" {
		t.Fatalf("%+v", peer)
	}
	var out bytes.Buffer
	if err := Print(&out, results, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claude worker: this read failed; using a good read", "claude peer: this read failed; using a good read"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("no %q in:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "read:failed") {
		t.Fatalf("a used read is no failed read:\n%s", out.String())
	}
}
