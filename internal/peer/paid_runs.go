package peer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// recordPeer records a new pane's entry; tests replace it to fail.
var recordPeer = func(reg *session.Registry, e session.Entry, limit int) error {
	return reg.RecordLimited(e, limit)
}

// accountTimeout bounds an account state write that must happen even after
// the command was cancelled: a record of a pane or a task that exists.
const accountTimeout = 10 * time.Second

// accounts opens the account gate that delegate uses, shared by every lead
// and project of the user, with this command's pane probe: one rex inspect
// per pane, no capture.
func (e *env) accounts() (*paidruns.Gate, error) {
	if e.gate != nil {
		return e.gate, nil
	}
	g, err := delegate.NewGate(e.cfg)
	if err != nil {
		return nil, err
	}
	g.Reconcile.Pane = e.paneProbe
	e.gate = g
	return g, nil
}

// paneProbe reports whether the pane of a reservation is there: Alive while
// its program runs, Gone once rex says the pane or its program ended, and
// Unknown when rex cannot tell.
func (e *env) paneProbe(ctx context.Context, o paidruns.Owner) paidruns.Presence {
	client := e.rex
	client.Anchor = ""
	if o.PeerSession != "" {
		client.Session = o.PeerSession
	}
	presence, err := client.Probe(ctx, o.Pane)
	switch {
	case err != nil:
		return paidruns.Unknown
	case presence == rex.Running:
		return paidruns.Alive
	case presence == rex.Gone, presence == rex.Exited:
		return paidruns.Gone
	}
	return paidruns.Unknown
}

// hold is one admitted reservation of a peer: a pane's slot with its first
// task, or a send's task.
type hold struct {
	gate   *paidruns.Gate
	permit paidruns.Permit
}

func (h *hold) id() string { return h.permit.Reservation.ID }

// launch marks the start before the pane exists or the task is typed, so a
// command that dies in between leaves the hold, never a proof of no start.
func (h *hold) launch(ctx context.Context) error {
	return h.gate.Store.Launch(ctx, h.id())
}

// attach records the pane that holds the reservation, even after the command
// was cancelled: the pane exists.
func (h *hold) attach(ctx context.Context, block, rexSession string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountTimeout)
	defer cancel()
	return h.gate.Store.Attach(ctx, h.id(), paidruns.Owner{Pane: block, PeerSession: rexSession})
}

// release gives back a hold whose pane never opened or whose task was proven
// never typed.
func (h *hold) release(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountTimeout)
	defer cancel()
	return h.gate.Finish(ctx, h.permit, paidruns.Settlement{})
}

// admitPane admits a new pane of route before it exists: one live slot and
// one task's estimate in one reservation, the free variant first and the
// paid one when the agent opted in. A pane never waits for a slot: a refusal
// names the counts and the close command.
func (e *env) admitPane(ctx context.Context, route router.Route, lead leadInfo, role, id string) (*hold, error) {
	g, err := e.accounts()
	if err != nil {
		return nil, err
	}
	variants := []workerenv.Variant{workerenv.Free}
	for _, c := range paidruns.Candidates([]config.Target{route.Target}, true, e.cfg.PaidRuns) {
		if c.Variant == workerenv.Paid {
			variants = append(variants, workerenv.Paid)
		}
	}
	var refusals []string
	for _, variant := range variants {
		in := peerInput(e.cfg, route, variant, lead.id, role, id, filepath.Dir(e.dir), e.opts.ProtectedRoots, os.Environ())
		permit, err := g.Acquire(ctx, paidruns.Request{Input: in, Family: models.Normalize(route.Agent, route.Model), Role: role,
			Owner: paidruns.ProcessOwner(os.Getpid()), OpenPeer: true})
		var refusal *paidruns.Refusal
		if errors.As(err, &refusal) {
			refusals = append(refusals, e.refusalLine(lead.id, refusal))
			continue
		}
		if err != nil {
			return nil, err
		}
		return &hold{gate: g, permit: permit}, nil
	}
	return nil, fmt.Errorf("%s -> lead: %s", role, strings.Join(refusals, "; "))
}

// refusalLine says why a pane or a send was refused, and for a full slot how
// to free one: the lead's open panes and the close command.
func (e *env) refusalLine(leadID string, r *paidruns.Refusal) string {
	line := delegate.RefusalLine(r)
	if !r.Slot() || r.Reason == paidruns.ReasonOneTask {
		return line
	}
	var open []string
	if all, err := e.reg.Entries(); err == nil {
		for _, x := range all {
			if x.Block != "" && !x.Closed && x.Lead == leadID {
				open = append(open, x.ID)
			}
		}
	}
	if len(open) == 0 {
		return line + "; close a pane with duolog peer close <id>"
	}
	return line + "; open: " + strings.Join(open, ", ") + "; close one with duolog peer close " + open[0]
}

// admitSend admits the task of a send to the pane's account session: the
// open's task when it still holds it, or a new task. A send takes no slot
// and never waits.
func (e *env) admitSend(ctx context.Context, entry session.Entry) (*hold, error) {
	if entry.AccountSessionID == "" {
		return nil, fmt.Errorf("peer %s was opened before duolog counted account sessions; close it with duolog peer close %s and open it again", entry.ID, entry.ID)
	}
	g, err := e.accounts()
	if err != nil {
		return nil, err
	}
	var open paidruns.Reservation
	if err := g.Store.Update(ctx, func(st *paidruns.State) error { open = st.Reservations[entry.AccountSessionID]; return nil }); err != nil {
		return nil, err
	}
	if !open.Session {
		return nil, fmt.Errorf("peer %s holds no account session; close it with duolog peer close %s and open it again", entry.ID, entry.ID)
	}
	route := router.Route{Target: config.Target{Agent: entry.Agent}, Context: e.cfg.Roles[entry.Role].Context}
	in := peerInput(e.cfg, route, open.Variant, entry.Lead, entry.Role, entry.ID, filepath.Dir(e.dir), e.opts.ProtectedRoots, os.Environ())
	permit, err := g.Acquire(ctx, paidruns.Request{Input: in, Family: open.Family, Role: entry.Role,
		Owner: paidruns.ProcessOwner(os.Getpid()), ExistingSession: entry.AccountSessionID})
	var refusal *paidruns.Refusal
	if errors.As(err, &refusal) {
		return nil, fmt.Errorf("peer %s: nothing was sent: %s", entry.ID, e.refusalLine(entry.Lead, refusal))
	}
	if err != nil {
		return nil, err
	}
	return &hold{gate: g, permit: permit}, nil
}

// taskEnded settles the pane's sent task, once: it takes the generation off
// the entry under the registry's lock, so of all the commands that see the
// task end, only one settles it. Peers report no token counts, so a paid
// task counts at its estimate; the returned note says so. A settlement that
// fails puts the generation back.
func (e *env) taskEnded(ctx context.Context, entry session.Entry) (string, error) {
	gen := ""
	if _, err := e.reg.Update(entry.ID, func(x *session.Entry) {
		gen, x.TaskGeneration = x.TaskGeneration, ""
	}); err != nil || gen == "" {
		return "", err
	}
	g, err := e.accounts()
	if err == nil {
		var task paidruns.Reservation
		if err = g.Store.Update(ctx, func(st *paidruns.State) error { task = st.Reservations[gen]; return nil }); err == nil {
			settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountTimeout)
			defer cancel()
			permit := paidruns.Permit{Reservation: paidruns.Reservation{ID: gen, Agent: entry.Agent}}
			if err = g.Finish(settleCtx, permit, paidruns.Settlement{Started: true}); err == nil {
				if task.Paid {
					return fmt.Sprintf("peers report no token counts, so the task counts at its estimate of %.2f USD", task.Estimate.USD), nil
				}
				return "", nil
			}
		}
	}
	_, restoreErr := e.reg.Update(entry.ID, func(x *session.Entry) {
		if x.TaskGeneration == "" {
			x.TaskGeneration = gen
		}
	})
	return "", errors.Join(err, restoreErr)
}

// sessionEnded frees the pane's slot once its pane is known to be gone or
// closed: a task the open still holds was never sent and is refunded, and a
// sent task that did not settle counts as a run of unknown cost, at least
// its estimate when paid.
func (e *env) sessionEnded(ctx context.Context, entry session.Entry) error {
	if entry.AccountSessionID == "" {
		return nil
	}
	g, err := e.accounts()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountTimeout)
	defer cancel()
	return g.CloseSession(ctx, entry.AccountSessionID)
}

// paneAbsent reports an open that rex refused or never reached, so no pane
// exists that could hold the reservation.
func paneAbsent(err error) bool {
	var rexErr *rex.Error
	return errors.As(err, &rexErr) || errors.Is(err, rex.ErrSocket)
}

// billingQuestion reports a question that chooses how the agent is billed,
// such as Claude asking whether to use an API key it found. Only the lead
// answers it, whatever auto_answers say.
func billingQuestion(s Screen) bool {
	text := strings.ToLower(s.Question + "\n" + s.Text)
	return strings.Contains(text, "api key") || strings.Contains(text, "api_key")
}
