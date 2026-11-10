package gate

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/store"
)

// AskResult is how one escalated ask ended, in closed names only: Answer is
// allow, deny or timeout; By is person or lead for an answer or a wait that
// ran out, auto when duolog denied without asking anyone; Wait is how long
// the worker waited.
type AskResult struct {
	Answer, By string
	Wait       time.Duration
}

type askObserverKey struct{}

// askObserver reports the first ask of one hook call and ignores the rest, so
// a hook that escalates again in the same call cannot count an ask twice.
type askObserver struct {
	once sync.Once
	f    func(AskResult)
}

// WithAskObserver returns a context whose Escalate calls report how their
// ask ended to f, once per context. The observer cannot change the decision:
// Escalate calls it after deciding and ignores what it does.
func WithAskObserver(ctx context.Context, f func(AskResult)) context.Context {
	if f == nil {
		return ctx
	}
	return context.WithValue(ctx, askObserverKey{}, &askObserver{f: f})
}

func observeAsk(ctx context.Context, r AskResult) {
	if o, ok := ctx.Value(askObserverKey{}).(*askObserver); ok {
		o.once.Do(func() { o.f(r) })
	}
}

// AskPoll is how often Escalate reads its inbox for the lead's reply. Tests
// shorten it; the hook keeps the default.
var AskPoll = 500 * time.Millisecond

// Escalate waits for a reply from the worker's spawning lead. Each request has
// its own inbox so simultaneous hook processes cannot consume each other's reply.
// The hook caller preserves the evaluated mode and logs the final decision.
func Escalate(ctx context.Context, b *bus.Bus, lead string, a policy.Action, timeout time.Duration, person bool) policy.Decision {
	started := time.Now()
	asked := "lead"
	if person {
		asked = "person"
	}
	// Each way out reports who settled the ask; nothing reads the reason back.
	settle := func(d policy.Decision, answer, by string) policy.Decision {
		observeAsk(ctx, AskResult{Answer: answer, By: by, Wait: time.Since(started)})
		return d
	}
	decision := func(kind, reason string) policy.Decision {
		return policy.Decision{Kind: kind, Reason: reason, Rule: "ask lead"}
	}
	deny := func(reason string) policy.Decision { return settle(decision(policy.Deny, reason), policy.Deny, "auto") }
	timedOut := func() policy.Decision {
		return settle(decision(policy.Deny, "ask timeout or cancellation"), "timeout", asked)
	}
	if lead == "" || session.CurrentLead(os.Environ()) != lead {
		return deny("ask requires the spawning DUOLOG_LEAD")
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if waitCtx.Err() != nil {
		return timedOut()
	}
	reg, err := session.OpenWithin(waitCtx, b.Directory())
	if err != nil {
		return deny("cannot read ask lead: " + err.Error())
	}
	l, alive, err := reg.Lead(lead)
	if err != nil || !alive {
		return deny("ask lead is unavailable or has ended")
	}
	if waiting, err := reg.IsWaiting(lead); err != nil {
		return deny("cannot read lead wait state")
	} else if waiting {
		// Name the command, so the lead can allow it or rewrite the task without reading the log.
		if a.Kind == "mcp" {
			return deny(fmt.Sprintf("lead is waiting for foreground delegate; MCP tool %q needs approval. Return done false with mcp:%s in needs; the lead may allow it with duolog remember mcp:%s or capabilities.mcp_tools", safeTarget(a.Target), safeTarget(a.Target), safeTarget(a.Target)))
		}
		return deny(fmt.Sprintf("lead is waiting for foreground delegate; %q needs approval. Return done false with it in needs, or use a plain command inside the project without pipes, globs or substitutions; the lead may ask the user to allow it next time with duolog remember", askSummary(strings.Join(strings.Fields(safeTarget(a.Target)), " "), 160)))
	}
	address := session.Address(l)
	from := "duolog/ask." + rand.Text()
	fullSummary := strings.Join(strings.Fields(a.Kind+" "+safeTarget(a.Target)), " ")
	// A peer pane names itself, so that peer wait stops only for its own asks.
	peer := os.Getenv("DUOLOG_PEER")
	// The wait, rounded up, tells readers when this hook has given up.
	wait := int((timeout + time.Second - 1) / time.Second)
	n, err := sendCut(b.Within(waitCtx), bus.Message{From: from, To: address, K: policy.Ask, Ref: peer, P: person, W: wait}, "", fullSummary)
	if err != nil {
		return deny("cannot send ask: " + err.Error())
	}
	ticker := time.NewTicker(AskPoll)
	defer ticker.Stop()
	// While it waits, the poll's locks wait no longer than the ask does: a
	// process that stalls with one cannot hold back the deny.
	poll := askPoll{reg: reg.Within(waitCtx), bus: b.Within(waitCtx), lead: lead, from: from, address: address, n: n}
	for {
		select {
		case <-waitCtx.Done():
		case <-ticker.C:
		}
		answer, err := "", error(nil)
		if waitCtx.Err() == nil {
			answer, err = poll.answer()
		}
		switch {
		case waitCtx.Err() != nil:
			return expire(ctx, b, bus.Message{From: from, To: address, Ref: peer}, n, fullSummary, timedOut())
		case errors.Is(err, errLeadGone):
			return deny("ask lead is unavailable or has ended")
		case err != nil:
			return deny("cannot read ask reply: " + err.Error())
		case answer != "":
			return settle(decision(answer, fmt.Sprintf("lead %s answered %s for ask %d", lead, answer, n)), answer, asked)
		}
	}
}

// errLeadGone is a poll that found the ask's lead unavailable or ended.
var errLeadGone = errors.New("ask lead is unavailable or has ended")

// askPoll reads the reply to ask n, sent from the ask's own inbox from to the
// lead's address.
type askPoll struct {
	reg                 *session.Registry
	bus                 *bus.Bus
	lead, from, address string
	n                   int64
}

// answer reads the inbox once and returns allow or deny when the lead has
// answered, and "" while it has not.
func (p askPoll) answer() (string, error) {
	if _, alive, err := p.reg.Lead(p.lead); err != nil || !alive {
		return "", errors.Join(errLeadGone, err)
	}
	lines, err := p.bus.Inbox(p.from)
	if err != nil {
		return "", err
	}
	ref := strconv.FormatInt(p.n, 10)
	for _, line := range lines {
		var m bus.Message
		if json.Unmarshal(line, &m) != nil || m.From != p.address || m.To != p.from || m.Ref != ref || m.N <= p.n {
			continue
		}
		if m.K == policy.Allow || m.K == policy.Deny {
			return m.K, nil
		}
	}
	return "", nil
}

// sendCut sends m with prefix and as much of text as the wire takes, cut by
// askSummary.
func sendCut(b *bus.Bus, m bus.Message, prefix, text string) (int64, error) {
	for budget := 200 - len(prefix); ; budget-- {
		m.S = prefix + askSummary(text, budget)
		n, err := b.Send(m)
		if !errors.Is(err, bus.ErrTooLong) || budget <= 0 {
			return n, err
		}
	}
}

// expire tells the lead in one line that ask n ran out of time and that its
// asker was denied, and returns that deny. The deny stands even when the line
// cannot be sent. A wait that ctx cancelled did not expire, so it sends none.
// The line waits for the bus lock at most store.NoticeWait, and less when ctx
// ends first; it is dropped then, so the deny is never held back.
func expire(ctx context.Context, b *bus.Bus, ask bus.Message, n int64, action string, d policy.Decision) policy.Decision {
	if ctx.Err() != nil {
		return d
	}
	notice, cancel := context.WithTimeout(ctx, store.NoticeWait)
	defer cancel()
	ref := strconv.FormatInt(n, 10)
	prefix := "ask " + ref + " from " + asker(ask.Ref) + " expired and was denied: "
	_, _ = sendCut(b.Within(notice), bus.Message{From: ask.From, To: ask.To, K: "status", Ref: ref}, prefix, action)
	return d
}

// asker names who sent an ask: its peer pane, or else the worker's role.
func asker(peer string) string {
	if peer != "" {
		return peer
	}
	if role := os.Getenv("DUOLOG_ROLE"); role != "" {
		return "worker " + role
	}
	return "a worker"
}

// askSummary visibly preserves both ends and points to the complete action.
func askSummary(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	marker := " ... [cut; duolog decisions] ... "
	if budget < len(marker)+2 {
		marker = " [cut] "
	}
	if budget <= len(marker) {
		return marker[:max(0, budget)]
	}
	head := (budget - len(marker)) / 2
	tail := len(text) - (budget - len(marker) - head)
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	for tail < len(text) && !utf8.RuneStart(text[tail]) {
		tail++
	}
	return text[:head] + marker + text[tail:]
}

const (
	// MaxAskWait is the longest wait for an ask a lead can answer: a lead that
	// works sees it within a turn, and its worker should not stall for longer.
	MaxAskWait = 120 * time.Second
	// MaxPersonAskWait is the longest wait for an ask only the person can
	// answer. With HookMargin it fits the timeout install writes.
	MaxPersonAskWait = 10 * time.Minute
	// HookMargin is how long before its hook's timeout a gate stops waiting.
	// An agent that kills a hook can let the tool run, so the gate has to
	// answer with a deny first.
	HookMargin = 30 * time.Second
	// LegacyHookBudget is the gate hook timeout that earlier installs wrote. It
	// stands in when no hooks file shows the hook's own timeout.
	LegacyHookBudget = MaxAskWait + HookMargin
)

// AskExpired reports that no gate can still be waiting for this ask: its asker's
// wait has passed. An ask from before asks carried their wait keeps the hook
// budget, which also covers a worker that was killed.
func AskExpired(m bus.Message) bool {
	at, err := time.Parse(time.RFC3339Nano, m.T)
	wait := LegacyHookBudget
	if m.W > 0 {
		wait = time.Duration(m.W) * time.Second
	}
	return err == nil && time.Since(at) > wait
}

// AskWait is how long one ask waits: the configured seconds, within the cap of
// its kind and with HookMargin to spare inside hook, the timeout of the hook
// that runs this gate. A hook too short to wait in gives no wait, so the ask
// denies at once.
func AskWait(seconds int, person bool, hook time.Duration) time.Duration {
	ceiling := MaxAskWait
	if person {
		ceiling = MaxPersonAskWait
	}
	return max(0, min(time.Duration(seconds)*time.Second, ceiling, hook-HookMargin))
}
