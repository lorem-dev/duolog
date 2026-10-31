package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/lorem-dev/duolog/internal/gate"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

func TestDecisionsUnderDepthRefused(t *testing.T) {
	gateSetup(t, mode.Ask)
	for _, kind := range []string{"allow", "deny"} {
		_, err := gateCommand(t, "", kind, "1")
		if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "workers cannot") {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}
func TestDecisionCommands(t *testing.T) {
	r, root := gateSetup(t, mode.Ask)
	t.Setenv("DUOLOG_DEPTH", "")
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r.Dir, cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"allow", "deny"} {
		n, err := b.Send(bus.Message{From: "codex/ask.test", To: "codex/lead.lead-1", K: "ask", S: "exec custom"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gateCommand(t, "", kind, strconv.FormatInt(n, 10)); err != nil {
			t.Fatal(err)
		}
		lines, err := b.Inbox("codex/ask.test")
		if err != nil || len(lines) != 1 {
			t.Fatal(lines, err)
		}
		var m bus.Message
		if err := json.Unmarshal(lines[0], &m); err != nil {
			t.Fatal(err)
		}
		if m.From != "codex/lead.lead-1" || m.K != kind || m.Ref != strconv.FormatInt(n, 10) {
			t.Fatal(m)
		}
	}
	for _, n := range []string{"0", "-1", "bad", "99999"} {
		if _, err := gateCommand(t, "", "allow", n); err == nil {
			t.Fatalf("accepted %s", n)
		}
	}
}

func TestAskHookLogsFinalOutcome(t *testing.T) {
	for _, outcome := range []string{"allow", "deny", "timeout", "ended", "missing lead"} {
		t.Run(outcome, func(t *testing.T) {
			r, root := gateSetup(t, mode.Ask)
			cfg, err := config.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			b, err := bus.Open(r.Dir, cfg.Wire)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "ended" {
				if err := r.Close("lead-123456"); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "missing lead" {
				t.Setenv("DUOLOG_LEAD", "")
			}
			replyDone := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if outcome == "allow" || outcome == "deny" {
				go func() {
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							replyDone <- ctx.Err()
							return
						case <-ticker.C:
						}
						lines, err := b.Inbox("codex/lead.lead-1")
						if err != nil {
							replyDone <- err
							return
						}
						for _, line := range lines {
							var request bus.Message
							if err := json.Unmarshal(line, &request); err != nil {
								replyDone <- err
								return
							}
							if request.K == "ask" {
								_, err = b.Send(bus.Message{From: "codex/lead.lead-1", To: request.From, K: outcome, Ref: strconv.FormatInt(request.N, 10), S: outcome})
								replyDone <- err
								return
							}
						}
					}
				}()
			}
			out := runGate(t, "custom-command")
			if outcome == "allow" {
				if out != "" {
					t.Fatal(out)
				}
			} else if !strings.Contains(out, `"permissionDecision":"deny"`) {
				t.Fatal(out)
			}
			if outcome == "allow" || outcome == "deny" {
				if err := <-replyDone; err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(filepath.Join(root, ".duolog/decisions.ndjson"))
			if err != nil {
				t.Fatal(err)
			}
			var record gate.Record
			if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
				t.Fatal(err)
			}
			want := gate.Deny
			if outcome == "allow" {
				want = gate.Allow
			}
			if record.Kind != want || record.Mode != mode.Ask || record.Action.Target != "custom-command" || record.Reason == "" || record.Rule == "" {
				t.Fatalf("record: %+v", record)
			}
			if outcome == "ended" || outcome == "missing lead" {
				data, err := os.ReadFile(filepath.Join(root, ".duolog/bus.ndjson"))
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(data) > 0 {
					t.Fatalf("unexpected message: %s", data)
				}
			}
		})
	}
}

func TestDecisionWithoutLeadEnvUsesFlag(t *testing.T) {
	r, root := gateSetup(t, mode.Ask)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r.Dir, cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	n, err := b.Send(bus.Message{From: "codex/ask.test", To: "codex/lead.lead-1", K: "ask", S: "exec custom"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateCommand(t, "", "allow", strconv.FormatInt(n, 10)); err == nil {
		t.Fatal("accepted without a lead")
	}
	if _, err := gateCommand(t, "", "allow", "--lead", "lead-123456", strconv.FormatInt(n, 10)); err != nil {
		t.Fatal(err)
	}
}
