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
func Escalate(ctx context.Context, b *bus.Bus, lead string, a Action, timeout time.Duration) Decision {
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
		return deny("lead is waiting for foreground delegate; return done false with needs, then re-run after the lead decides")
	}
	address := session.Address(l)
	from := "duolog/ask." + rand.Text()
	fullSummary := strings.Join(strings.Fields(a.Kind+" "+a.Target), " ")
	var n int64
	for budget := 200; ; budget-- {
		summary := askSummary(fullSummary, budget)
		n, err = b.Send(bus.Message{From: from, To: address, K: Ask, S: summary})
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

// AskExpired reports that no gate can still be waiting for this ask, whether it
// timed out or its worker was killed.
func AskExpired(sent string) bool {
	at, err := time.Parse(time.RFC3339Nano, sent)
	return err == nil && time.Since(at) > MaxAskWait+30*time.Second
}

// AskWait clamps project settings to the shipped hook budget.
func AskWait(seconds int) time.Duration { return min(time.Duration(seconds)*time.Second, MaxAskWait) }
