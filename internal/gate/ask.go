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
	address := session.Address(l)
	from := "duolog/ask." + rand.Text()
	summary := strings.Join(strings.Fields(a.Kind+" "+a.Target), " ")
	// Send is authoritative for both configured byte limits and JSON escaping.
	// Retry only size failures; a failed Send never appends a message.
	for len(summary) > 200 {
		summary = shorterSummary(summary)
	}
	var n int64
	for {
		n, err = b.Send(bus.Message{From: from, To: address, K: Ask, S: summary})
		if !errors.Is(err, bus.ErrTooLong) || summary == "" {
			break
		}
		summary = shorterSummary(summary)
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

func shorterSummary(s string) string {
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}
