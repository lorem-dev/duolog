// Package runsnap is the versioned snapshot of a lead's workers that
// `duolog runs --snapshot` prints: its live peers, its delegate runs, the
// asks open to it and its load. Version 1 keeps its field names; later
// changes only add fields.
package runsnap

import (
	"fmt"
	"strings"
	"time"
)

// Version is the snapshot version this package writes.
const Version = 1

// Snapshot is one reading of a lead's workers. Every path in it is relative
// to the lead home's project root, with "/" separators.
type Snapshot struct {
	Version int `json:"version"`
	// GeneratedAt is the time of the reading, RFC 3339 in UTC.
	GeneratedAt string `json:"generated_at"`
	// LineText is Line(), so that a reader needs no Go call. Go allows no
	// field named as the method, so the field has another name.
	LineText string `json:"line"`
	// Lead is nil when no lead resolves.
	Lead *Lead `json:"lead"`
	Rex  Rex   `json:"rex"`
	// Peers and Runs are never null in a built snapshot.
	Peers    []Peer `json:"peers"`
	Runs     []Run  `json:"runs"`
	AsksOpen int    `json:"asks_open"`
	Load     Load   `json:"load"`
	// Error names each part that could not be read, as "part: reason".
	Error string `json:"error,omitempty"`
}

// Lead is the lead whose workers the snapshot reads.
type Lead struct {
	ID    string `json:"id"`
	Agent string `json:"agent"`
}

// Rex says whether peers could be asked for.
type Rex struct {
	Available bool `json:"available"`
}

// Peer is one live peer of the lead. Model is empty when not known.
type Peer struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	State    string `json:"state"`
	Working  bool   `json:"working"`
	Task     string `json:"task"`
	Tab      string `json:"tab"`
	Worktree string `json:"worktree"`
	Last     *Last  `json:"last"`
}

// Last is a peer's last event; At is nil when not known.
type Last struct {
	Kind string     `json:"kind"`
	At   *time.Time `json:"at"`
}

// Run is one delegate run of the lead. HasLog says whether its log exists;
// the log's path is not part of the snapshot.
type Run struct {
	ID       string     `json:"id"`
	Role     string     `json:"role"`
	Agent    string     `json:"agent"`
	Model    string     `json:"model"`
	State    string     `json:"state"`
	Started  *time.Time `json:"started"`
	Ended    *time.Time `json:"ended"`
	Worktree string     `json:"worktree"`
	TaskDir  string     `json:"task_dir"`
	HasLog   bool       `json:"has_log"`
	Line     string     `json:"line"`
}

// Load is the lead's load, read from cached numbers only. Window5h and
// Weekly are percentages, nil when not known.
type Load struct {
	Agents   []AgentLoad `json:"agents"`
	Queued   int         `json:"queued"`
	Window5h *float64    `json:"window_5h"`
	Weekly   *float64    `json:"weekly"`
}

// AgentLoad is one agent's load: Running and Idle are the lead's live
// peers, Limit and Live the account's sessions.
type AgentLoad struct {
	Agent   string `json:"agent"`
	Running int    `json:"running"`
	Idle    int    `json:"idle"`
	Limit   int    `json:"limit"`
	Live    int    `json:"live"`
}

// Line is the snapshot in one short line, such as "duolog 2 peers 1 run 1
// ask 3 queued": the live peers, the running runs, the open asks and the
// queued items, each left out when there is none. It is "" when there is
// nothing to show.
func (s Snapshot) Line() string {
	peers, runs := 0, 0
	for _, p := range s.Peers {
		if p.State != "exited" && p.State != "gone" && p.State != "closed" {
			peers++
		}
	}
	for _, r := range s.Runs {
		if r.State == "running" {
			runs++
		}
	}
	var parts []string
	for _, c := range []struct {
		n    int
		name string
	}{{peers, "peer"}, {runs, "run"}, {s.AsksOpen, "ask"}, {s.Load.Queued, "queued"}} {
		if c.n == 0 {
			continue
		}
		name := c.name
		if c.n > 1 && name != "queued" {
			name += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", c.n, name))
	}
	if len(parts) == 0 {
		return ""
	}
	return "duolog " + strings.Join(parts, " ")
}
