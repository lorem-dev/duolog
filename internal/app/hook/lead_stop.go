package hook

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/project"
)

// stopEvents is the optional hook output of an agent that can block a Stop.
// Agents without it keep their existing Stop behavior.
type stopEvents interface {
	BlockStop(reason string) []byte
}

// leadStop gives a Claude lead one chance to start a background wait for an
// unwatched busy peer before yielding. A repeated Stop must not trap the lead
// in a loop. The peer branch of Stop remains best effort and never blocks.
func leadStop(ctx context.Context, e Env) Output {
	events, canBlock := e.Events.(stopEvents)
	id := stoppingLead(e.Input)
	dir := project.LeadHome(e.Root)
	if ctx.Err() != nil || !canBlock || id == "" || !folderExists(dir) {
		return Output{}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	reg, err := session.OpenWithin(ctx, dir)
	if err != nil {
		return Output{}
	}
	lead, alive, err := reg.Lead(id)
	if err != nil || !alive || lead.Agent != e.Agent {
		return Output{}
	}
	lines, err := unwatchedPeers(ctx, e, reg, lead)
	if err != nil {
		return Output{}
	}
	// Completion may race with the lead's last tool call. Deliver it here so
	// a peer already ready at Stop does not need another prompt to be noticed.
	cfg, err := loadConfig(e)
	if err != nil {
		return Output{}
	}
	b, err := bus.Open(dir, cfg.Wire)
	if err != nil {
		return Output{}
	}
	data, err := take(ctx, e, &state{cfg: cfg, reg: reg, bus: b.Within(ctx), lead: lead})
	if err != nil {
		return Output{}
	}
	if len(data) > 0 {
		lines = append([]string{string(data)}, lines...)
	}
	if len(lines) == 0 {
		return Output{}
	}
	return Output{Stdout: events.BlockStop(strings.Join(lines, "\n"))}
}

// stoppingLead is the session id of a Stop a lead may be held at, or "" for
// an unreadable payload, a subagent, or a Stop already held once.
func stoppingLead(input []byte) string {
	var h struct {
		ID     string `json:"session_id"`
		Agent  string `json:"agent_id"`
		Active bool   `json:"stop_hook_active"`
	}
	if json.Unmarshal(input, &h) != nil || h.Agent != "" || h.Active {
		return ""
	}
	return h.ID
}

func folderExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// unwatchedPeers is one instruction for each open peer of this lead that is
// busy with no wait on it.
func unwatchedPeers(ctx context.Context, e Env, reg *session.Registry, lead session.Lead) ([]string, error) {
	entries, err := reg.Entries()
	if err != nil || e.Peers == nil {
		return nil, err
	}
	var lines []string
	for _, line := range e.Peers(ctx, lead.ID) {
		id, state, ok := strings.Cut(line, " ")
		if !ok || (state != "starting" && state != "working" && state != "updating" && state != "question") {
			continue
		}
		own := slices.ContainsFunc(entries, func(x session.Entry) bool { return x.ID == id && x.Lead == lead.ID && !x.Closed })
		if !own {
			continue
		}
		watched, err := reg.Watched(id)
		if err != nil {
			return nil, err
		}
		if !watched {
			lines = append(lines, "Peer "+id+" is "+state+" without an active wait. Run `duolog peer wait "+id+" --lead "+lead.ID+"` as its own background command with completion notification before ending this turn. If it returns a gate ask, handle or relay it, then restart the background wait with `--after-ask N` for that ask's number. Never answer person-only asks yourself.")
		}
	}
	return lines, nil
}
