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
	"time"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/session"
)

// Escalate waits for a reply from the worker's spawning lead. Each request has
// its own inbox so simultaneous hook processes cannot consume each other's reply.
// The hook caller preserves the evaluated mode and logs the final decision.
func Escalate(ctx context.Context, b *bus.Bus, lead string, a Action, timeout time.Duration, person bool) Decision {
	decision := func(kind, reason string) Decision {
		return Decision{Kind: kind, Reason: reason, Rule: "ask lead"}
	}
	deny := func(reason string) Decision { return decision(Deny, reason) }
	if lead == "" || session.CurrentLead(os.Environ()) != lead {
		return deny("ask requires the spawning DUOLOG_LEAD")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if ctx.Err() != nil {
		return deny("ask timeout or cancellation")
	}
	reg, err := session.Open(b.Directory())
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
			return deny(fmt.Sprintf("lead is waiting for foreground delegate; MCP tool %q needs approval. Return done false with mcp:%s in needs; the lead may allow it with duolog remember mcp:%s or capabilities.mcp_tools", a.Target, a.Target, a.Target))
		}
		return deny(fmt.Sprintf("lead is waiting for foreground delegate; %q needs approval. Return done false with it in needs, or use a plain command inside the project without pipes, globs or substitutions; the lead may ask the user to allow it next time with duolog remember", askSummary(strings.Join(strings.Fields(a.Target), " "), 160)))
	}
	address := session.Address(l)
	from := "duolog/ask." + rand.Text()
	fullSummary := strings.Join(strings.Fields(a.Kind+" "+a.Target), " ")
	// A peer pane names itself, so that peer wait stops only for its own asks.
	peer := os.Getenv("DUOLOG_PEER")
	// The wait, rounded up, tells readers when this hook has given up.
	wait := int((timeout + time.Second - 1) / time.Second)
	var n int64
	for budget := 200; ; budget-- {
		summary := askSummary(fullSummary, budget)
		n, err = b.Send(bus.Message{From: from, To: address, K: Ask, Ref: peer, S: summary, P: person, W: wait})
		if !errors.Is(err, bus.ErrTooLong) || budget == 0 {
			break
		}
	}
	if err != nil {
		return deny("cannot send ask: " + err.Error())
	}
	ref := strconv.FormatInt(n, 10)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return deny("ask timeout or cancellation")
		case <-ticker.C:
		}
		if ctx.Err() != nil {
			return deny("ask timeout or cancellation")
		}
		if _, alive, err := reg.Lead(lead); err != nil || !alive {
			return deny("ask lead is unavailable or has ended")
		}
		lines, err := b.Inbox(from)
		if err != nil {
			return deny("cannot read ask reply: " + err.Error())
		}
		for _, line := range lines {
			var m bus.Message
			if json.Unmarshal(line, &m) != nil || m.From != address || m.To != from || m.Ref != ref || m.N <= n {
				continue
			}
			if m.K == Allow || m.K == Deny {
				return decision(m.K, fmt.Sprintf("lead %s answered %s for ask %d", lead, m.K, n))
			}
		}
	}
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

// MaxAskWait fits below the 150-second timeout of every shipped gate hook.
const MaxAskWait = 120 * time.Second

// AskExpired reports that no gate can still be waiting for this ask: its asker's
// wait has passed. An ask from before asks carried their wait keeps the hook
// budget, which also covers a worker that was killed.
func AskExpired(m bus.Message) bool {
	at, err := time.Parse(time.RFC3339Nano, m.T)
	wait := MaxAskWait + 30*time.Second
	if m.W > 0 {
		wait = time.Duration(m.W) * time.Second
	}
	return err == nil && time.Since(at) > wait
}

// AskWait clamps project settings to the shipped hook budget.
func AskWait(seconds int) time.Duration { return min(time.Duration(seconds)*time.Second, MaxAskWait) }
