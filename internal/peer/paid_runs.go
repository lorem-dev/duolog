package peer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// recordPeer records a new pane's entry; tests replace it to fail.
var recordPeer = func(reg *session.Registry, e session.Entry, limit int) error {
	return reg.RecordLimited(e, limit)
}

// openedEnvFile runs once rex answered an open, before its env file is
// checked; tests replace it to stand for a launcher that took the file.
var openedEnvFile = func(string) {}

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
// token, when set, is the pane's env file: while it is there to take, the
// launcher started nothing, which reconciliation can prove.
func (h *hold) launch(ctx context.Context, token string) error {
	return h.gate.Store.LaunchWithToken(ctx, h.id(), token)
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
func (e *env) admitPane(ctx context.Context, route coreroute.Route, lead leadInfo, role, id, run string) (*hold, error) {
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
		in, err := e.paneInput(e.root, route, lead.mode, variant, lead.id, role, id, run)
		if err != nil {
			return nil, err
		}
		permit, err := g.Acquire(ctx, paidruns.Request{Input: in, Family: coreroute.Normalize(route.Agent, route.Model), Role: role,
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

// paneInput describes the pane of route in variant that runs in root, with
// the places its settings layers come from: the home folder, the project
// root, this platform and the environment the pane gets, so the gate reads
// the layers the pane will. The pane's environment is the free build's: the
// variables that move the layers, such as CLAUDE_CONFIG_DIR and CODEX_HOME,
// are never route names, so both variants have the same. m is the lead's
// mode, which picks the pane's native permission flags.
func (e *env) paneInput(root string, route coreroute.Route, m mode.Mode, variant workerenv.Variant, leadID, role, id, run string) (workerenv.ProcessInput, error) {
	in := peerInput(e.cfg, route, m, variant, leadID, role, id, run, filepath.Dir(e.dir), e.opts.ProtectedRoots, os.Environ())
	free := in
	free.Variant = workerenv.Free
	built, err := agentEnv(free)
	if err != nil {
		return workerenv.ProcessInput{}, err
	}
	home, _ := os.UserHomeDir()
	in.Locations = workerenv.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: built.Env}
	return in, nil
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
	// The send is built as the pane was, so the gate can tell whether it is
	// still on the pane's route; it is admitted on the pane's billing class.
	route := coreroute.Route{Target: config.Target{Agent: entry.Agent}, Context: e.cfg.Roles[entry.Role].Context}
	root := entry.WorkRoot
	if root == "" {
		root = e.root
	}
	in, err := e.paneInput(root, route, entry.Mode, open.Variant, entry.Lead, entry.Role, entry.ID, entry.Run)
	if err != nil {
		return nil, err
	}
	permit, err := g.Acquire(ctx, paidruns.Request{Input: in, Family: open.Family, Role: entry.Role,
		Owner: paidruns.ProcessOwner(os.Getpid()), ExistingSession: entry.AccountSessionID})
	var refusal *paidruns.Refusal
	if errors.As(err, &refusal) && refusal.Reason == paidruns.ReasonPaneRoute {
		return nil, fmt.Errorf("peer %s: nothing was sent: %s, such as an API key or a settings file that this command sees otherwise; close it with duolog peer close %s and open it again",
			entry.ID, refusal.Reason, entry.ID)
	}
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

// Release ends the account holds of a peer pane that no entry records, or
// any hold named by its reservation id, as quota and doctor show them: the
// slot and the estimate of an open that died after the pane's launcher took
// its env file. It refuses a pane that its entry still records, which peer
// close ends, and a hold whose pane, launcher or command still runs.
func Release(ctx context.Context, id string) (string, error) {
	e, err := setupFor(ctx, false)
	if err != nil {
		return "", err
	}
	if entry, err := e.find(id); err == nil && !entry.Closed {
		return "", fmt.Errorf("peer %s is open; close it with duolog peer close %s", entry.ID, entry.ID)
	}
	g, err := e.accounts()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountTimeout)
	defer cancel()
	ended, err := g.Store.EndHold(ctx, id, g.Reconcile)
	if err != nil {
		return "", err
	}
	usd := 0.0
	for _, r := range ended {
		if r.Paid && (r.Shape != workerenv.Peer || !r.Session) {
			usd += r.Estimate.USD
		}
	}
	line := fmt.Sprintf("%s released: %d hold(s) ended", id, len(ended))
	if usd > 0 {
		line += fmt.Sprintf("; a sent task counts at its estimate of %.2f USD", usd)
	}
	return line, nil
}

// paneAbsent reports an open that rex refused or never reached, so no pane
// exists that could hold the reservation.
func paneAbsent(err error) bool {
	var rexErr *rex.Error
	return errors.As(err, &rexErr) || errors.Is(err, rex.ErrSocket)
}

// billingTerms are the words that make a question one that may spend money,
// in lower case.
var billingTerms = []string{"api key", "bill", "credit", "gateway", "payment", "purchase", "overage",
	"usage-based", "usage based", "pay-as-you-go", "pay as you go", "extra usage"}

// billingQuestion reports a question that may lead to spending, such as
// Claude asking whether to use an API key it found or to turn on extra
// usage. Only the lead answers it, whatever auto_answers say. The question
// decides, not the rest of the screen: its asking sentence or an option that
// names an API key, billing, credits, a gateway, a payment or purchase,
// overage, usage-based or pay-as-you-go pricing or extra usage, or a
// credential variable shown with it. Output above the question that only
// mentions one leaves the configured answers on.
func billingQuestion(s Screen) bool {
	q := strings.ToLower(s.Question)
	if strings.Contains(q, "api_key") {
		return true
	}
	ask := q
	if i := strings.Index(q, "?"); i >= 0 {
		ask = q[:i]
		if j := strings.LastIndexAny(ask, ".!•"); j >= 0 {
			ask = ask[j+1:]
		}
	}
	for _, o := range s.Options {
		ask += "\n" + strings.ToLower(o.Label)
	}
	return slices.ContainsFunc(billingTerms, func(term string) bool { return strings.Contains(ask, term) })
}
