package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

// allowEveryAsk answers each ask on the project's bus with allow from the
// gateSetup lead, the way `duolog allow N` does, until the test ends.
func allowEveryAsk(t *testing.T, root string) {
	t.Helper()
	old := gate.AskPoll
	gate.AskPoll = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		<-done
		gate.AskPoll = old
	})
	dir := filepath.Join(root, ".duolog")
	go func() {
		defer close(done)
		answered := map[int64]bool{}
		for ctx.Err() == nil {
			time.Sleep(5 * time.Millisecond)
			data, err := os.ReadFile(filepath.Join(dir, "bus.ndjson"))
			if err != nil {
				continue
			}
			for _, line := range bytes.Split(data, []byte("\n")) {
				var m bus.Message
				if json.Unmarshal(line, &m) != nil || m.K != "ask" || answered[m.N] {
					continue
				}
				b, err := bus.Open(dir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
				if err != nil {
					continue
				}
				if _, err := b.Send(bus.Message{From: "codex/lead.lead-1", To: m.From, K: policy.Allow, Ref: strconv.FormatInt(m.N, 10), S: policy.Allow}); err == nil {
					answered[m.N] = true
				}
			}
		}
	}()
}

// Plan's limit is a hard one: a command that is not a proven read, or a write
// outside the project, is denied before anything asks, so no allow runs it.
func TestPlanDeniesBeforeAnyAllowCanAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, agent, tool string
		input             map[string]string
	}{
		{"uncertain-command", "codex", "Bash", map[string]string{"command": "env TASK_FLAG=1 touch marker.txt"}},
		{"outside-write", "claude", "Write", map[string]string{"file_path": "../marker.txt", "content": "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := gateSetup(t, mode.Plan)
			allowEveryAsk(t, root)
			data, _ := json.Marshal(map[string]any{"session_id": "worker", "cwd": root, "tool_name": tc.tool, "tool_input": tc.input})
			out, err := gateCommand(t, string(data), "gate", "--agent", tc.agent, "--event", "PreToolUse")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "plan mode permits only reads") {
				t.Fatal("plan let an allow decide:", out)
			}
			if bus, _ := os.ReadFile(filepath.Join(root, ".duolog", "bus.ndjson")); bytes.Contains(bus, []byte(`"k":"ask"`)) {
				t.Fatal("plan asked:", string(bus))
			}
		})
	}
}
