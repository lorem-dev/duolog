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
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/gate"
)

const (
	// showAll is the most messages one hook prints as lines.
	showAll = 5
	// showNewest is how many a hook prints when there are more than showAll.
	showNewest = 3
	// summaryRunes is the longest summary a line carries.
	summaryRunes = 80
)

// line is one short plain line for a message, in place of its JSON.
//
//	ask 15287 peer-x: exec go test ./... (person)
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
	case gate.Ask:
		out := "ask " + strconv.FormatInt(m.N, 10) + " " + who + ": " + s
		if m.P {
			out += " (person)"
		}
		return out
	case "status", "notice":
		return m.K + " " + who + ": " + s
	}
	kind := m.K
	if kind == "" {
		kind = "msg"
	}
	return kind + " " + strconv.FormatInt(m.N, 10) + " " + m.From + ": " + s
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
		if json.Unmarshal(raw, &m) == nil && (m.K == gate.Allow || m.K == gate.Deny) {
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
		asks = asks || m.K == gate.Ask
		out = append(out, entry{raw, m})
	}
	return out, asks
}

// present turns the lines an earlier hook held and the lead's unread lines
// into the text of one hook and the lines to keep held. An ask that was
// answered or has expired is dropped: nobody can act on it. Every open ask is
// shown, a held one too. More than showAll other new messages show only the
// newest showNewest of them; the rest join the held lines. While any line is
// held, the text ends in one line that counts them.
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
	for _, e := range old {
		switch {
		case e.m.K != gate.Ask:
			keep = append(keep, e.raw)
		case !closed(e.m):
			out.WriteString(line(e.m) + "\n")
		}
	}
	shown, newlyHeld := capOthers(fresh, closed)
	for _, e := range shown {
		out.WriteString(line(e.m) + "\n")
	}
	keep = append(keep, newlyHeld...)
	if len(keep) > 0 {
		out.WriteString("and " + strconv.Itoa(len(keep)) + " more: duolog inbox\n")
	}
	return out.Bytes(), keep
}

// capOthers drops closed asks from fresh and splits the rest into the lines
// to show and the lines to hold. An open ask is always shown: its asker waits
// for the lead. More than showAll other lines hold all but the newest
// showNewest of them.
func capOthers(fresh []entry, closed func(bus.Message) bool) (shown []entry, held [][]byte) {
	others := 0
	for _, e := range fresh {
		if e.m.K == gate.Ask && closed(e.m) {
			continue
		}
		if e.m.K != gate.Ask {
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
		if e.m.K != gate.Ask && hold > 0 {
			hold--
			held = append(held, e.raw)
			continue
		}
		shown = append(shown, e)
	}
	return shown, held
}
