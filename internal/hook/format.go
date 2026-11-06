package hook

import (
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
	decoder := json.NewDecoder(f)
	for {
		var m bus.Message
		if err := decoder.Decode(&m); err == io.EOF {
			return answered, nil
		} else if err != nil {
			// A torn tail or a line of another shape holds no answer.
			return answered, nil
		}
		if m.K == gate.Allow || m.K == gate.Deny {
			answered[m.Ref] = true
		}
	}
}

// present turns the lines of a lead's unread messages into the text of one
// hook and the lines it did not show. An ask that was answered or has expired
// is dropped: nobody can act on it. More than showAll messages show only the
// newest showNewest and one line that names the rest, which stay in the
// inbox.
func present(dir string, unread [][]byte) (text []byte, held [][]byte) {
	type entry struct {
		raw []byte
		m   bus.Message
	}
	var entries []entry
	asks := false
	for _, raw := range unread {
		var m bus.Message
		if json.Unmarshal(raw, &m) != nil {
			m = bus.Message{S: string(bytes.TrimSpace(raw))}
		}
		asks = asks || m.K == gate.Ask
		entries = append(entries, entry{raw, m})
	}
	var answered map[string]bool
	if asks {
		answered, _ = answeredAsks(dir)
	}
	var shown []entry
	for _, e := range entries {
		if e.m.K == gate.Ask && (gate.AskExpired(e.m) || answered[strconv.FormatInt(e.m.N, 10)]) {
			continue
		}
		shown = append(shown, e)
	}
	more := 0
	if len(shown) > showAll {
		more = len(shown) - showNewest
		for _, e := range shown[:more] {
			held = append(held, e.raw)
		}
		shown = shown[more:]
	}
	var out bytes.Buffer
	for _, e := range shown {
		out.WriteString(line(e.m) + "\n")
	}
	if more > 0 {
		out.WriteString("and " + strconv.Itoa(more) + " more: duolog inbox\n")
	}
	return out.Bytes(), held
}
