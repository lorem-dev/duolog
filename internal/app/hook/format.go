package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

const (
	// showAll is the most messages one hook prints as lines.
	showAll = 5
	// showNewest is how many a hook prints when there are more than showAll.
	showNewest = 3
	// summaryRunes is the longest summary a line carries.
	summaryRunes = 80
)

// clock is the time the lines count an ask's wait from; tests set it.
var clock = time.Now

// line is one short plain line for a message, in place of its JSON. An ask
// ends in how to answer it and, when it carries its wait, the seconds left
// before its asker is denied.
//
//	ask 15287 peer-x: exec go test ./...; duolog allow 15287 or duolog deny 15287, 95s left
//	ask 7 peer-x: exec cat ../x (person); the person runs duolog allow 7 or duolog deny 7, 95s left
//	status peer-x: pane closed
//	notice duolog/plugin: plugin offered
//	done 6 codex/review: finished
func line(m bus.Message) string {
	who := m.Ref
	if who == "" {
		who = m.From
	}
	s := cut(m.S, summaryRunes)
	switch m.K {
	case policy.Ask:
		n := strconv.FormatInt(m.N, 10)
		out := "ask " + n + " " + who + ": " + s
		answer := "duolog allow " + n + " or duolog deny " + n
		if m.P {
			out += " (person)"
			answer = "the person runs " + answer
		}
		out += "; " + answer
		if left, ok := askLeft(m); ok {
			out += ", " + strconv.Itoa(left) + "s left"
		}
		return out
	case "status", "notice", "peer-idle", "peer-exit":
		return m.K + " " + who + ": " + s
	}
	kind := m.K
	if kind == "" {
		kind = "msg"
	}
	return kind + " " + strconv.FormatInt(m.N, 10) + " " + m.From + ": " + s
}

// expires reports whether an ask carries a time that gate.AskExpired can count
// from. Only such an ask is held for the next hook; one without it would show
// in every hook until an answer came, so it is shown once.
func expires(m bus.Message) bool {
	_, err := time.Parse(time.RFC3339Nano, m.T)
	return err == nil
}

// askLeft returns the whole seconds, rounded up, before the asker of m stops
// waiting. ok is false for an ask that does not carry its wait or time.
func askLeft(m bus.Message) (int, bool) {
	at, err := time.Parse(time.RFC3339Nano, m.T)
	if err != nil || m.W <= 0 {
		return 0, false
	}
	left := at.Add(time.Duration(m.W) * time.Second).Sub(clock())
	return max(0, int((left+time.Second-1)/time.Second)), true
}

// cut joins the lines of s and keeps at most n characters, ending in ... when
// it cut.
func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-3]) + "..."
}

// answeredAsks lists the numbers of the asks that have an allow or a deny.
func answeredAsks(dir string) (map[string]bool, error) {
	f, err := os.Open(filepath.Join(dir, "bus.ndjson"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	answered := map[string]bool{}
	input := bufio.NewReader(f)
	for {
		raw, err := input.ReadBytes('\n')
		var m bus.Message
		// A torn tail or a line of another shape holds no answer; the lines
		// after it still count.
		if json.Unmarshal(raw, &m) == nil && (m.K == policy.Allow || m.K == policy.Deny) {
			answered[m.Ref] = true
		}
		if err == io.EOF {
			return answered, nil
		}
		if err != nil {
			return answered, err
		}
	}
}

// entry is one bus line with its decoded message.
type entry struct {
	raw []byte
	m   bus.Message
}

// entries decodes lines; a line of another shape becomes a plain message.
func entries(lines [][]byte) (out []entry, asks bool) {
	for _, raw := range lines {
		var m bus.Message
		if json.Unmarshal(raw, &m) != nil {
			m = bus.Message{S: string(bytes.TrimSpace(raw))}
		}
		asks = asks || m.K == policy.Ask
		out = append(out, entry{raw, m})
	}
	return out, asks
}

// present turns the lines an earlier hook held and the lead's unread lines
// into the text of one hook and the lines to keep held. An ask that was
// answered or has expired is dropped: nobody can act on it. Every open ask is
// shown, and held, so that each later hook shows it again until it is answered
// or expires; one without a time to expire from is shown once. More than showAll other new messages show only the newest
// showNewest of them; the rest join the held lines. While any of those other
// lines is held, the text ends in one line that counts them.
func present(dir string, held, unread [][]byte) (text []byte, keep [][]byte) {
	old, oldAsks := entries(held)
	fresh, freshAsks := entries(unread)
	var answered map[string]bool
	if oldAsks || freshAsks {
		answered, _ = answeredAsks(dir)
	}
	closed := func(m bus.Message) bool {
		return gate.AskExpired(m) || answered[strconv.FormatInt(m.N, 10)]
	}
	var out bytes.Buffer
	var asks, others [][]byte
	for _, e := range old {
		switch {
		case e.m.K != policy.Ask:
			others = append(others, e.raw)
		case !closed(e.m):
			out.WriteString(line(e.m) + "\n")
			if expires(e.m) {
				asks = append(asks, e.raw)
			}
		}
	}
	shown, newlyHeld := capOthers(fresh, closed)
	for _, e := range shown {
		out.WriteString(line(e.m) + "\n")
		if e.m.K == policy.Ask && expires(e.m) {
			asks = append(asks, e.raw)
		}
	}
	others = append(others, newlyHeld...)
	if len(others) > 0 {
		out.WriteString("and " + strconv.Itoa(len(others)) + " more: duolog inbox\n")
	}
	return out.Bytes(), append(asks, others...)
}

// capOthers drops closed asks from fresh and splits the rest into the lines
// to show and the lines to hold. An open ask is always shown: its asker waits
// for the lead. More than showAll other lines hold all but the newest
// showNewest of them.
func capOthers(fresh []entry, closed func(bus.Message) bool) (shown []entry, held [][]byte) {
	others := 0
	for _, e := range fresh {
		if e.m.K == policy.Ask && closed(e.m) {
			continue
		}
		if e.m.K != policy.Ask {
			others++
		}
		shown = append(shown, e)
	}
	hold := 0
	if others > showAll {
		hold = others - showNewest
	}
	all := shown
	shown = nil
	for _, e := range all {
		if e.m.K != policy.Ask && hold > 0 {
			hold--
			held = append(held, e.raw)
			continue
		}
		shown = append(shown, e)
	}
	return shown, held
}
