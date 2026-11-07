package paidruns

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// paceStateFile keeps, per agent, whether the last balance decision found it
// ahead of pace, for the hysteresis of route.Ahead.
const paceStateFile = "pace.json"

// PaceBalance reads both agents' quota with read, as processes from build,
// and returns the balance for this call, or nil when routing.balance is off.
// An agent whose build or read fails is left out: its quota is unknown. A
// switched-off agent is left out too, with no build and no read. dir holds
// the hysteresis state, see SavePaceState.
func PaceBalance(ctx context.Context, cfg *config.Config,
	read func(context.Context, launch.Process) (account.Snapshot, error),
	build func(agent string) (launch.Process, error),
	dir string, now time.Time) *route.Balance {
	if cfg == nil || cfg.Routing.Balance != "pace" {
		return nil
	}
	b := &route.Balance{Mode: cfg.Routing.Balance, Margin: cfg.Routing.Margin,
		Pace: map[string]route.AgentPace{}, Was: loadPaceState(dir)}
	for _, agent := range paidrules.PaceAgents() {
		if cfg.AgentSwitchedOff(agent) {
			continue
		}
		p, err := build(agent)
		if err != nil {
			continue
		}
		s, readErr := read(ctx, p)
		b.Pace[agent] = paidrules.AgentPace(p, s, readErr, cfg, now)
	}
	return b
}

// PaceRead reads the quota a process sees for DefaultPaceBalance. Tests of
// callers replace it, so that no test runs a real agent.
var PaceRead = paceRead

// paceRead is one read without the retry of a timed-out read: the balance
// reads both agents one after the other on the way to a route, and an agent
// whose read fails is only left out, so a loaded machine waits one budget
// per agent. Only the account gate's read retries.
func paceRead(ctx context.Context, p launch.Process) (account.Snapshot, error) {
	return paceReader.ReadProcess(ctx, p)
}

// paceReader is the reader of paceRead; tests shorten its timeout.
var paceReader = quota.Reader{Once: true}

// DefaultPaceBalance is PaceBalance for the agents as a free worker sees
// them, in the user's account state folder. The worker is built from the
// person's config, so a proxy or CA variable in env_passthrough reaches the
// read; the free variant still withholds every route name.
func DefaultPaceBalance(ctx context.Context, cfg *config.Config) *route.Balance {
	if cfg == nil || cfg.Routing.Balance != "pace" {
		return nil
	}
	dir, err := os.UserConfigDir()
	if err == nil {
		dir = filepath.Join(dir, "duolog", "accounts")
	}
	build := func(agent string) (launch.Process, error) {
		return workerenv.Build(launch.ProcessInput{Agent: agent, Shape: launch.Worker,
			Variant: launch.Free, Config: cfg, Environ: os.Environ()})
	}
	return PaceBalance(ctx, cfg, PaceRead, build, dir, time.Now())
}

// SaveDefaultPaceState keeps the ahead state of a routed task in the user's
// account state folder.
func SaveDefaultPaceState(ahead map[string]bool) {
	if dir, err := os.UserConfigDir(); err == nil {
		SavePaceState(filepath.Join(dir, "duolog", "accounts"), ahead)
	}
}

// loadPaceState reads the ahead state in dir; a missing or unreadable file is
// an empty state, which only ends a hysteresis early.
func loadPaceState(dir string) map[string]bool {
	out := map[string]bool{}
	if dir == "" {
		return out
	}
	data, err := os.ReadFile(filepath.Join(dir, paceStateFile))
	if err != nil {
		return out
	}
	var file struct {
		Ahead map[string]bool `json:"ahead"`
	}
	if json.Unmarshal(data, &file) != nil {
		return out
	}
	for agent, ahead := range file.Ahead {
		out[agent] = ahead
	}
	return out
}

// SavePaceState merges ahead into the state in dir, making the folder
// private to the user. It is best effort: a lost write only ends a
// hysteresis early, so nothing is reported. Nothing is written for an empty
// ahead or dir.
func SavePaceState(dir string, ahead map[string]bool) {
	if dir == "" || len(ahead) == 0 || !filepath.IsAbs(dir) {
		return
	}
	state := loadPaceState(dir)
	for agent, v := range ahead {
		state[agent] = v
	}
	data, err := json.Marshal(struct {
		Ahead map[string]bool `json:"ahead"`
	}{state})
	if err != nil || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, paceStateFile+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr != nil || cerr != nil || store.Rename(tmp.Name(), filepath.Join(dir, paceStateFile)) != nil {
		_ = os.Remove(tmp.Name())
	}
}
