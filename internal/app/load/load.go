// Package load builds the load line a lead reads: its live peers, the session
// limit of each agent and the 5-hour window, so the number of peers follows
// what the account allows.
package load

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Agent is the load of one agent. Running is the live peers of this lead and
// Idle the ones among them that finished their turn. Limit and Live are the
// numbers `duolog quota` prints as "<agent> sessions Live of Limit": Live
// counts the sessions of every lead.
type Agent struct {
	Name                       string
	Running, Idle, Limit, Live int
}

// Summary is what the line says. Idle names the idle peers of this lead.
type Summary struct {
	Agents      []Agent
	Idle        []string
	Window      float64
	WindowKnown bool
}

// windowCeiling is the 5-hour use, in percent, from which the line stops
// advising more work.
const windowCeiling = 70

// Line says the summary in one line, or "" when there is nothing to say.
// queueEmpty is whether no ready item waits in the lead's queue.
func (s Summary) Line(queueEmpty bool) string {
	var parts []string
	advice, most := "", 0
	for _, a := range s.Agents {
		if a.Limit <= 0 {
			continue
		}
		part := fmt.Sprintf("%s %d", a.Name, a.Running)
		if a.Running > 0 {
			part += " running"
			if a.Idle > 0 {
				part += fmt.Sprintf(" (%d idle)", a.Idle)
			}
		}
		parts = append(parts, part+fmt.Sprintf(" of %d", a.Limit))
		if free := a.Limit - a.Live; free*2 >= a.Limit && free > most {
			advice, most = a.Name, free
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if s.WindowKnown {
		parts = append(parts, fmt.Sprintf("5h %g%%", s.Window))
	}
	if len(s.Idle) > 0 {
		parts = append(parts, "idle: "+strings.Join(s.Idle, ", "))
	}
	line := "duolog peers: " + strings.Join(parts, "; ")
	if advice != "" && queueEmpty && !(s.WindowKnown && s.Window >= windowCeiling) {
		line += fmt.Sprintf(" - %d %s sessions free: start independent work or stack it on open pull request branches (duolog peer queue add)", most, advice)
	}
	return line
}

// QueueEmpty reports that no item waits in the lead's ready queue: the queue
// file in the state folder holds no item in the "queued" state. A missing or
// unreadable file counts as empty.
func QueueEmpty(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "peer-queue.ndjson"))
	if err != nil {
		return true
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scan.Scan() {
		var item struct {
			State string `json:"state"`
		}
		if json.Unmarshal(scan.Bytes(), &item) == nil && item.State == "queued" {
			return false
		}
	}
	return true
}

// every is how long the same line stays quiet on a bus delivery.
const every = 5 * time.Minute

type shown struct {
	Line string    `json:"line"`
	At   time.Time `json:"at"`
}

func shownPath(dir string) string { return filepath.Join(dir, "load-line.json") }

func readShown(dir string) map[string]shown {
	seen := map[string]shown{}
	if data, err := os.ReadFile(shownPath(dir)); err == nil {
		_ = json.Unmarshal(data, &seen)
	}
	return seen
}

// Mark records that the lead was shown line at now.
func Mark(dir, leadID, line string, now time.Time) {
	seen := readShown(dir)
	seen[leadID] = shown{Line: line, At: now}
	if data, err := json.Marshal(seen); err == nil {
		_ = os.WriteFile(shownPath(dir), data, 0o600)
	}
}

// Due says whether a bus delivery may carry line now: it was not shown to the
// lead within the last 5 minutes, or a number in it changed. A due line is
// recorded as shown.
func Due(dir, leadID, line string, now time.Time) bool {
	last, ok := readShown(dir)[leadID]
	if ok && last.Line == line && now.Sub(last.At) < every && !now.Before(last.At) {
		return false
	}
	Mark(dir, leadID, line, now)
	return true
}
