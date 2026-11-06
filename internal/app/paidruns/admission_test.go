package paidruns

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const (
	helperMode      = "DUOLOG_TEST_PAIDRUNS_HELPER"
	helperDir       = "DUOLOG_TEST_PAIDRUNS_DIR"
	helperBarrier   = "DUOLOG_TEST_PAIDRUNS_BARRIER"
	helperIndex     = "DUOLOG_TEST_PAIDRUNS_INDEX"
	helperNow       = "DUOLOG_TEST_PAIDRUNS_NOW"
	helperAdmission = "DUOLOG_TEST_PAIDRUNS_ADMISSION"
)

// TestAdmissionHelperProcess is not a test: the admission tests start the
// test binary with it as a separate process. "admit" waits at the barrier,
// admits its request against the shared state and prints the decision;
// "wait" stands for a running paid task until its stdin closes.
func TestAdmissionHelperProcess(t *testing.T) {
	switch os.Getenv(helperMode) {
	case "admit":
		os.Exit(helperAdmit())
	case "wait":
		fmt.Println("running")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	t.Skip("helper process only")
}

func helperAdmit() int {
	var in Admission
	if err := json.Unmarshal([]byte(os.Getenv(helperAdmission)), &in); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	now, err := time.Parse(time.RFC3339Nano, os.Getenv(helperNow))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	st, err := OpenUserState(os.Getenv(helperDir))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	st.Now = func() time.Time { return now }
	barrier := os.Getenv(helperBarrier)
	if err := os.WriteFile(filepath.Join(barrier, "ready-"+os.Getenv(helperIndex)), nil, 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for deadline := time.Now().Add(30 * time.Second); ; {
		if _, err := os.Stat(filepath.Join(barrier, "go")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "barrier never opened")
			return 2
		}
		time.Sleep(time.Millisecond)
	}
	d, err := st.Admit(context.Background(), in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := json.NewEncoder(os.Stdout).Encode(d); err != nil {
		return 2
	}
	return 0
}

// raceAdmissions starts one helper process per request, each in one of two
// unrelated project folders, lets them all through one barrier at once and
// returns their decisions in request order.
func raceAdmissions(t *testing.T, dir string, now time.Time, requests []Admission) []paidrules.Decision {
	t.Helper()
	roots := []string{t.TempDir(), t.TempDir()}
	barrier := t.TempDir()
	cmds := make([]*exec.Cmd, len(requests))
	outs := make([]*bytes.Buffer, len(requests))
	for i, in := range requests {
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestAdmissionHelperProcess$")
		cmd.Dir = roots[i%2]
		cmd.Env = append(os.Environ(), helperMode+"=admit", helperDir+"="+dir, helperBarrier+"="+barrier,
			helperIndex+"="+strconv.Itoa(i), helperNow+"="+now.Format(time.RFC3339Nano), helperAdmission+"="+string(data))
		outs[i] = &bytes.Buffer{}
		cmd.Stdout = outs[i]
		cmd.Stderr = outs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = cmd
	}
	defer func() {
		for _, cmd := range cmds {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	}()
	for deadline := time.Now().Add(60 * time.Second); ; {
		ready := 0
		for i := range requests {
			if _, err := os.Stat(filepath.Join(barrier, "ready-"+strconv.Itoa(i))); err == nil {
				ready++
			}
		}
		if ready == len(requests) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d helpers reached the barrier", ready, len(requests))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(barrier, "go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	decisions := make([]paidrules.Decision, len(requests))
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outs[i])
		}
		if err := json.Unmarshal(outs[i].Bytes(), &decisions[i]); err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outs[i])
		}
	}
	// Neither project folder holds any account state.
	for _, root := range roots {
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Fatalf("account state written into a project: %v", entries)
		}
	}
	return decisions
}

// oneWinner checks that exactly one decision admitted and that every other
// refused with reason, and returns the winner's index.
func oneWinner(t *testing.T, decisions []paidrules.Decision, reason string) int {
	t.Helper()
	winner := -1
	for i, d := range decisions {
		switch {
		case d.Admitted && winner >= 0:
			t.Fatalf("requests %d and %d both admitted: %+v", winner, i, decisions)
		case d.Admitted:
			winner = i
		case d.Reason != reason:
			t.Fatalf("request %d refused with %q, want %q: %+v", i, d.Reason, reason, d)
		}
	}
	if winner < 0 {
		t.Fatalf("no request admitted: %+v", decisions)
	}
	return winner
}

// The clock of the admission tests, close to a midnight so that a
// calendar-day reset would show.
var ledgerNow = time.Date(2026, 11, 5, 23, 50, 0, 0, time.UTC)

func testStore(t *testing.T, now *time.Time) *Store {
	t.Helper()
	st, err := OpenUserState(filepath.Join(t.TempDir(), "accounts"))
	if err != nil {
		t.Fatal(err)
	}
	st.Now = func() time.Time { return *now }
	return st
}

func subscriptionAssessment(agent string, variant workerenv.Variant, account string) paidrules.Assessment {
	share := 1.0
	if account == "workspace" {
		share = 0.5
	}
	return paidrules.Assessment{Agent: agent, Shape: workerenv.Worker, Variant: variant, Verdict: paidrules.Subscription,
		Account: account, UsableShare: share}
}

func paidAssessment(agent string) paidrules.Assessment {
	return paidrules.Assessment{Agent: agent, Shape: workerenv.Worker, Variant: workerenv.Paid, Verdict: paidrules.Paid,
		Account: "personal", UsableShare: 1,
		Signals: []workerenv.Signal{{Name: "ANTHROPIC_API_KEY", Layer: "environment"}}}
}

// window is a five-hour window that a subscription task fits in.
func window(now time.Time) []paidrules.WindowCost {
	return []paidrules.WindowCost{{Key: paidrules.WindowKey{Kind: "five_hour", Reset: now.Add(3 * time.Hour)}, Used: 0.2, Cost: 0.05, Reserve: 0.10}}
}

// workerTask is a worker delegation: one live session running one task.
func workerTask(id, agent string, usd float64, windows []paidrules.WindowCost) paidrules.Reservation {
	return paidrules.Reservation{ID: id, Agent: agent, Role: "implement", Family: "sonnet", Shape: workerenv.Worker,
		Supervisor: paidrules.Owner{PID: 4242, Birth: "b-4242"}, Session: true, Task: true,
		Estimate: paidrules.Estimate{Input: 1_275_000, Output: 225_000, USD: usd, Windows: windows, Source: "test"}}
}

func paidTask(id, agent string, usd float64) Admission {
	r := workerTask(id, agent, usd, nil)
	r.Variant = workerenv.Paid
	return Admission{Reservation: r, Assessment: paidAssessment(agent), DailyCapUSD: 5, TotalLimit: 10, AgentLimit: 10}
}

func freeTask(id, agent, account string, now time.Time) Admission {
	r := workerTask(id, agent, 1, window(now))
	r.Variant = workerenv.Free
	return Admission{Reservation: r, Assessment: subscriptionAssessment(agent, workerenv.Free, account),
		TotalLimit: 10, AgentLimit: 10}
}

func mustAdmit(t *testing.T, st *Store, in Admission) paidrules.Decision {
	t.Helper()
	d, err := st.Admit(context.Background(), in)
	if err != nil {
		t.Fatal(in.Reservation.ID, err)
	}
	if !d.Admitted {
		t.Fatalf("%s refused: %+v", in.Reservation.ID, d)
	}
	return d
}

func mustRefuse(t *testing.T, st *Store, in Admission, reason string) paidrules.Decision {
	t.Helper()
	before := readState(t, st)
	d, err := st.Admit(context.Background(), in)
	if err != nil {
		t.Fatal(in.Reservation.ID, err)
	}
	if d.Admitted || d.Reason != reason {
		t.Fatalf("%s: %+v, want refused with %q", in.Reservation.ID, d, reason)
	}
	if after := readState(t, st); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused %s changed the state:\n%+v\n%+v", in.Reservation.ID, before, after)
	}
	return d
}

func spent(t *testing.T, st *Store, agent string, now time.Time) (float64, float64) {
	t.Helper()
	settled, inflight, err := st.Spent(context.Background(), agent, now)
	if err != nil {
		t.Fatal(err)
	}
	return settled, inflight
}

// liveSessions counts the held sessions of agent, or of every agent.
func liveSessions(s State, agent string) int {
	n := 0
	for _, r := range s.Reservations {
		if r.Session && (agent == "" || r.Agent == agent) {
			n++
		}
	}
	return n
}

func TestAdmissionCrossProcessLastCapAndSlot(t *testing.T) {
	ctx := context.Background()
	now := ledgerNow

	t.Run("last USD headroom", func(t *testing.T) {
		st := testStore(t, &now)
		// 3 USD spent an hour ago leaves 2 USD of the 5 USD cap: each
		// request fits exactly, but only one of them can have it.
		err := st.Update(ctx, func(s *State) error {
			s.Settled["earlier"] = paidrules.Settlement{ID: "earlier", Agent: "claude", ChargedUSD: 3, Ended: now.Add(-time.Hour)}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		var requests []Admission
		for i := range 4 {
			requests = append(requests, paidTask(fmt.Sprintf("paid-%d", i), "claude", 2))
		}
		decisions := raceAdmissions(t, st.Dir, now, requests)
		winner := oneWinner(t, decisions, ReasonCap)
		s := readState(t, st)
		if len(s.Reservations) != 1 || len(s.Settled) != 1 {
			t.Fatalf("losers changed the ledger: %+v", s)
		}
		got, ok := s.Reservations[requests[winner].Reservation.ID]
		if !ok || !got.Paid || got.Started || !got.Created.Equal(now) {
			t.Fatalf("winner's reservation %+v", got)
		}
		// The admission at exactly the cap holds the whole cap.
		if settled, inflight := spent(t, st, "claude", now); settled != 3 || inflight != 2 {
			t.Fatalf("spent %v + %v, want 3 + 2", settled, inflight)
		}
		mustRefuse(t, st, paidTask("one-cent-more", "claude", 0.01), ReasonCap)
	})

	t.Run("equal to cap", func(t *testing.T) {
		st := testStore(t, &now)
		in := paidTask("whole-cap", "claude", 5)
		d := mustAdmit(t, st, in)
		if !d.Reservation.Paid || d.Reservation.ID != "whole-cap" {
			t.Fatalf("decision %+v", d)
		}
		in = paidTask("above-cap", "codex", 5.01)
		mustRefuse(t, st, in, ReasonCap)
	})

	t.Run("last agent slot", func(t *testing.T) {
		st := testStore(t, &now)
		mustAdmit(t, st, freeTask("held", "claude", "personal", now))
		var requests []Admission
		for i := range 4 {
			in := freeTask(fmt.Sprintf("agent-slot-%d", i), "claude", "personal", now)
			in.AgentLimit = 2
			requests = append(requests, in)
		}
		before := readState(t, st)
		decisions := raceAdmissions(t, st.Dir, now, requests)
		winner := oneWinner(t, decisions, ReasonAgentSessions)
		for i, d := range decisions {
			if i != winner && (d.Live != 2 || d.Limit != 2) {
				t.Fatalf("loser %d: sessions %d of %d, want 2 of 2", i, d.Live, d.Limit)
			}
		}
		s := readState(t, st)
		if liveSessions(s, "claude") != 2 || len(s.Reservations) != len(before.Reservations)+1 ||
			!reflect.DeepEqual(s.Settled, before.Settled) {
			t.Fatalf("losers changed the slots or the ledger: %+v", s)
		}
	})

	t.Run("last total slot", func(t *testing.T) {
		st := testStore(t, &now)
		mustAdmit(t, st, freeTask("codex-held", "codex", "personal", now))
		var requests []Admission
		for i := range 4 {
			in := freeTask(fmt.Sprintf("total-slot-%d", i), "claude", "personal", now)
			in.TotalLimit = 2
			requests = append(requests, in)
		}
		decisions := raceAdmissions(t, st.Dir, now, requests)
		winner := oneWinner(t, decisions, ReasonTotalSessions)
		for i, d := range decisions {
			if i != winner && (d.Live != 2 || d.Limit != 2) {
				t.Fatalf("loser %d: sessions %d of %d, want 2 of 2", i, d.Live, d.Limit)
			}
		}
		if s := readState(t, st); liveSessions(s, "") != 2 || len(s.Reservations) != 2 {
			t.Fatalf("losers changed the slots: %+v", s)
		}
	})

	// Paid and workspace agents run one task at a time even when their
	// tier permits three sessions.
	for _, tc := range []struct {
		name string
		make func(id string) Admission
	}{
		{"paid", func(id string) Admission {
			in := paidTask(id, "claude", 0.5)
			in.AgentLimit = 3
			return in
		}},
		{"workspace", func(id string) Admission {
			in := freeTask(id, "claude", "workspace", now)
			in.AgentLimit = 3
			return in
		}},
	} {
		t.Run("one task "+tc.name, func(t *testing.T) {
			st := testStore(t, &now)
			var requests []Admission
			for i := range 4 {
				requests = append(requests, tc.make(fmt.Sprintf("%s-%d", tc.name, i)))
			}
			decisions := raceAdmissions(t, st.Dir, now, requests)
			winner := oneWinner(t, decisions, ReasonOneTask)
			s := readState(t, st)
			if len(s.Reservations) != 1 || len(s.Settled) != 0 {
				t.Fatalf("losers changed the state: %+v", s)
			}
			r := s.Reservations[requests[winner].Reservation.ID]
			if tc.name == "paid" && !r.Paid || tc.name == "workspace" && (!r.Workspace || r.Paid) {
				t.Fatalf("winner's flags %+v", r)
			}

			// Idle panes may fill the tier, but a task waits for the one
			// that runs.
			mustRefuse(t, st, tc.make("busy-elsewhere"), ReasonOneTask)
			pane := func(id string) Admission {
				in := tc.make(id)
				in.Reservation.Shape, in.Assessment.Shape = workerenv.Peer, workerenv.Peer
				in.Reservation.Task, in.Reservation.Estimate = false, paidrules.Estimate{}
				return in
			}
			mustAdmit(t, st, pane("pane-1"))
			d := mustAdmit(t, st, pane("pane-2"))
			if d.Live != 3 || d.Limit != 3 {
				t.Fatalf("idle panes: sessions %d of %d, want 3 of 3", d.Live, d.Limit)
			}
			mustRefuse(t, st, pane("pane-3"), ReasonAgentSessions)
			send := tc.make("send-1")
			send.Reservation.Shape, send.Assessment.Shape = workerenv.Peer, workerenv.Peer
			send.Reservation.Session, send.Reservation.SessionID = false, "pane-1"
			mustRefuse(t, st, send, ReasonOneTask)

			// Once the running task settles, a send on an idle pane runs; it
			// takes no new slot.
			if err := st.Settle(ctx, paidrules.Settlement{ID: requests[winner].Reservation.ID, Agent: "claude", PricedUSD: 0.25,
				Started: true, CompleteUsage: true}); err != nil {
				t.Fatal(err)
			}
			d = mustAdmit(t, st, send)
			if d.Live != 2 || d.Limit != 3 {
				t.Fatalf("send: sessions %d of %d, want 2 of 3", d.Live, d.Limit)
			}
			orphan := send
			orphan.Reservation.ID, orphan.Reservation.SessionID = "send-orphan", "no-such-pane"
			mustRefuse(t, st, orphan, ReasonNoSession)
		})
	}

	t.Run("opt-in, verdict and windows", func(t *testing.T) {
		st := testStore(t, &now)
		// A paid verdict never runs in the free variant, and the paid
		// variant needs the agent's cap.
		free := freeTask("free-but-paid", "claude", "personal", now)
		free.Assessment.Verdict = paidrules.Paid
		free.DailyCapUSD = 5
		mustRefuse(t, st, free, ReasonFreeVariantPaid)
		notAllowed := paidTask("not-allowed", "claude", 1)
		notAllowed.DailyCapUSD = 0
		mustRefuse(t, st, notAllowed, ReasonNotAllowed)
		// The window, scaled by the usable share, must keep its reserve:
		// at u = 0.5 and the weekly reserve 0.15, used plus cost may reach
		// 0.425 of the real window and no more.
		in := freeTask("tight", "claude", "workspace", now)
		in.Reservation.Estimate.Windows = []paidrules.WindowCost{{Key: paidrules.WindowKey{Kind: "seven_day", Reset: now.Add(48 * time.Hour)},
			Used: 0.3, Cost: 0.13, Reserve: 0.15}}
		mustRefuse(t, st, in, ReasonWindow)
		in.Reservation.Estimate.Windows[0].Cost = 0.125
		mustAdmit(t, st, in)
		// Absent window evidence is never free headroom, and overage on
		// keeps the same reserve.
		none := freeTask("no-window", "codex", "personal", now)
		none.Reservation.Estimate.Windows = nil
		mustRefuse(t, st, none, ReasonWindow)
		over := freeTask("overage", "codex", "personal", now)
		over.Assessment.Verdict = paidrules.SubscriptionOverage
		over.Reservation.Estimate.Windows[0].Used = 0.86
		mustRefuse(t, st, over, ReasonWindow)
		// A task in flight in the same window counts as reserved there.
		first := freeTask("first", "codex", "personal", now)
		first.Reservation.Estimate.Windows[0].Cost = 0.3
		mustAdmit(t, st, first)
		second := freeTask("second", "codex", "personal", now)
		second.Reservation.Estimate.Windows[0].Cost = 0.45
		mustRefuse(t, st, second, ReasonWindow)
	})

	t.Run("state errors refuse", func(t *testing.T) {
		st := testStore(t, &now)
		path := filepath.Join(st.Dir, "state.json")
		if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
			t.Fatal(err)
		}
		d, err := st.Admit(ctx, paidTask("corrupt", "claude", 1))
		if err == nil || d.Admitted {
			t.Fatalf("corrupt state admitted: %+v %v", d, err)
		}
		if _, _, err := st.Spent(ctx, "claude", now); err == nil {
			t.Fatal("corrupt state reported spend")
		}
		if data, _ := os.ReadFile(path); string(data) != "{not json" {
			t.Fatalf("corrupt state replaced: %s", data)
		}
	})

	t.Run("invalid requests", func(t *testing.T) {
		st := testStore(t, &now)
		bad := map[string]func(*Admission){
			"no id":          func(in *Admission) { in.Reservation.ID = "" },
			"other agent":    func(in *Admission) { in.Assessment.Agent = "codex" },
			"other shape":    func(in *Admission) { in.Assessment.Shape = workerenv.Peer },
			"lead":           func(in *Admission) { in.Reservation.Shape, in.Assessment.Shape = workerenv.Lead, workerenv.Lead },
			"no verdict":     func(in *Admission) { in.Assessment.Verdict = "" },
			"bad share":      func(in *Admission) { in.Assessment.UsableShare = 0 },
			"no limit":       func(in *Admission) { in.AgentLimit = 0 },
			"zero usd":       func(in *Admission) { in.Reservation.Estimate.USD = 0 },
			"negative usd":   func(in *Admission) { in.Reservation.Estimate.USD = -1 },
			"nothing held":   func(in *Admission) { in.Reservation.Session, in.Reservation.Task = false, false },
			"started":        func(in *Admission) { in.Reservation.Started = true },
			"bad cap":        func(in *Admission) { in.DailyCapUSD = -1 },
			"negative token": func(in *Admission) { in.Reservation.Estimate.Output = -1 },
		}
		for name, change := range bad {
			in := paidTask("bad", "claude", 1)
			change(&in)
			if d, err := st.Admit(ctx, in); err == nil || d.Admitted {
				t.Fatalf("%s: %+v %v", name, d, err)
			}
		}
		mustAdmit(t, st, paidTask("dup", "claude", 1))
		if d, err := st.Admit(ctx, paidTask("dup", "claude", 1)); err == nil || d.Admitted {
			t.Fatalf("duplicate id: %+v %v", d, err)
		}
		if s := readState(t, st); len(s.Reservations) != 1 {
			t.Fatalf("invalid requests changed the state: %+v", s)
		}
	})

	t.Run("attach", func(t *testing.T) {
		st := testStore(t, &now)
		mustAdmit(t, st, paidTask("run", "claude", 1))
		if err := st.Attach(ctx, "run", paidrules.Owner{}); err == nil {
			t.Fatal("attached a child without an identity")
		}
		if err := st.Attach(ctx, "missing", paidrules.Owner{PID: 7, Birth: "b-7"}); err == nil {
			t.Fatal("attached an unknown reservation")
		}
		child := paidrules.Owner{PID: 7, Birth: "b-7"}
		for range 2 {
			if err := st.Attach(ctx, "run", child); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.Attach(ctx, "run", paidrules.Owner{PID: 8, Birth: "b-8"}); err == nil {
			t.Fatal("a second child replaced the first")
		}
		r := readState(t, st).Reservations["run"]
		if !r.Started || r.Child != child || r.Supervisor.PID != 4242 {
			t.Fatalf("attached reservation %+v", r)
		}
	})
}

// waitingTask starts a helper process that stands for a running paid task;
// it ends normally once stdin closes.
func waitingTask(t *testing.T) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAdmissionHelperProcess$")
	cmd.Env = append(os.Environ(), helperMode+"=wait")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "running\n" {
		t.Fatalf("helper did not start: %q %v", line, err)
	}
	return cmd, stdin
}

func exitedNormally(t *testing.T, cmd *exec.Cmd, stdin io.WriteCloser) {
	t.Helper()
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("running task was stopped: %v", err)
	}
}
