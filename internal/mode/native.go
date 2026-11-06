package mode

import (
	"encoding/json"
	"slices"
	"strconv"
)

// Rank orders the native names duolog offers a worker. Claude: plan 0,
// default 1, acceptEdits 2, auto 3, bypassPermissions 4. Codex: read-only 0,
// workspace-write 2. Every other name is not offered: (-1, false). Claude
// plan stays ranked so that a config entry can lower to it; a worker asking
// it starts in default (launchNative).
func Rank(agent, native string) (int, bool) {
	switch agent {
	case "claude":
		switch native {
		case "plan":
			return 0, true
		case "default":
			return 1, true
		case "acceptEdits":
			return 2, true
		case "auto":
			return 3, true
		case "bypassPermissions":
			return 4, true
		}
	case "codex":
		switch native {
		case "read-only":
			return 0, true
		case "workspace-write":
			return 2, true
		}
	}
	return -1, false
}

// Default is the native value Args passes: the role sandbox narrows the
// mode, and full uses the same value as edits. A Claude worker or peer at
// level plan gets Claude's default mode, never Claude's plan (see
// launchNative).
func Default(agent string, m Mode, sandbox string) string {
	if sandbox == "read-only" {
		m = Plan
	}
	switch agent {
	case "codex":
		if m == Plan {
			return "read-only"
		}
		return "workspace-write"
	case "claude":
		switch m {
		case Edits, Full:
			return "acceptEdits"
		}
		return "default"
	}
	return ""
}

// launchNative is the native name a worker or peer starts with for the name
// duolog resolved. A Claude worker or peer never starts in Claude's plan
// mode: plan is an interactive mode, and leaving it, or a plan the agent
// wants to write, waits on an "Exit plan mode?" prompt for a person that a
// worker or peer does not have, so it deadlocks. It starts in Claude's
// default mode instead, one rank above plan.
//
// Claude's plan refused every tool that is not read-only before it ran. In
// default, duolog's gate is what does that, so such a worker or peer is held
// by the gate (GateHeld), and three things stand in for plan:
//
//   - The gate, at level plan or in a read-only role, denies every write and
//     every command that is not a named read-only one, except a peer's own
//     report. It also denies an MCP tool that no rule marks read_only and is
//     not a default read-only tool, and every tool it cannot judge as a read
//     or a write (EnterPlanMode included), where it would otherwise ask.
//   - The worker or peer starts without the built-in tools that are not
//     read-only and that the gate cannot judge (HeldDisallowedTools).
//   - It starts only after a gate preflight in its own environment shows the
//     gate runs: hooks present, not turned off, the gate command on PATH.
//
// The ceilings compare levels and the ranks of requested names, never this
// launch name. Codex's read-only is a real OS sandbox, not a prompt, and
// stays.
func launchNative(agent, native string) string {
	if agent == "claude" && native == "plan" {
		return "default"
	}
	return native
}

// GateHeld reports a worker or peer that only duolog's gate keeps read-only:
// Claude at level plan or with a read-only role sandbox, which starts in
// Claude's default mode (launchNative). It may start only where its gate is
// shown to run. This holds apart from Resolved.Looser: such a worker runs
// duolog's own default for its level.
func GateHeld(agent string, level Mode, sandbox string) bool {
	return agent == "claude" && (level == Plan || sandbox == "read-only")
}

// HeldDisallowedTools are Claude's built-in tools that a held worker or peer
// (GateHeld) starts without, beside Agent and Task: those that are not
// read-only and that the gate cannot judge as a read or a write, which it
// denies there, and the edit tools a held agent has no use for. A worker
// also starts without Write and Edit; a peer keeps them for its own report,
// which the gate allows and judges like any write. This is a second wall
// behind the gate, so a tool missing here is still denied by it.
func HeldDisallowedTools(peer bool) []string {
	tools := []string{"CronCreate", "CronDelete", "EnterPlanMode", "ExitPlanMode", "EnterWorktree", "ExitWorktree",
		"Monitor", "MultiEdit", "NotebookEdit", "PushNotification", "RemoteTrigger", "ScheduleWakeup", "SendMessage", "Workflow"}
	if !peer {
		tools = append(tools, "Edit", "Write")
	}
	return tools
}

// Flags turns a native name into the agent's command-line flags. A Claude
// plan becomes default (launchNative), whichever caller names it.
func Flags(agent, native string) []string {
	switch agent {
	case "codex":
		return []string{"-s", native}
	case "claude":
		return []string{"--permission-mode", launchNative(agent, native)}
	}
	return nil
}

// PinCodexSandbox adds the network and writable-roots keys to the flags of a
// Codex workspace-write launch, so that Codex's own config files (a cloned
// repository's .codex/config.toml included) cannot widen the sandbox. Other
// flags come back unchanged. roots nil means none.
func PinCodexSandbox(agent string, args []string, network bool, roots []string) []string {
	if agent != "codex" || len(args) != 2 || args[0] != "-s" || args[1] != "workspace-write" {
		return args
	}
	if roots == nil {
		roots = []string{}
	}
	// A JSON array of strings is a TOML array of basic strings.
	list, err := json.Marshal(roots)
	if err != nil {
		list = []byte("[]")
	}
	return append(slices.Clone(args),
		"-c", "sandbox_workspace_write.network_access="+strconv.FormatBool(network),
		"-c", "sandbox_workspace_write.writable_roots="+string(list))
}

// Ceiling is what the lead allows a worker.
type Ceiling struct {
	Lead, Effective Mode
	// Auto reports that a Claude worker may use auto: the lead is at full,
	// or is itself in auto at level edits.
	Auto bool
}

// NewCeiling builds the ceiling from the lead's observed mode and native
// name and the effective mode (the lead's, lowered by duolog mode).
func NewCeiling(leadAgent, leadNative string, lead, effective Mode) Ceiling {
	return Ceiling{
		Lead:      lead,
		Effective: effective,
		Auto:      effective == Full || leadAgent == "claude" && leadNative == "auto" && effective == Edits,
	}
}

// Request is the native name a config entry asks for. Native is empty when
// no entry applies; Source says where the entry came from.
type Request struct{ Native, Source string }

// Resolved is the level and native name a worker starts with.
type Resolved struct {
	Level     Mode   `json:"level"`
	Native    string `json:"native"`
	Source    string `json:"source"`
	LoweredBy string `json:"lowered_by,omitempty"`
	// Looser reports a native name above duolog's own default for the level.
	Looser bool `json:"-"`
}

// loosestRank is the highest native rank a level allows.
func loosestRank(agent string, level Mode, auto bool) int {
	if agent == "codex" {
		if level == Plan {
			return 0
		}
		return 2
	}
	switch level {
	case Plan:
		return 0
	case Edits:
		if auto {
			return 3
		}
		return 2
	case Full:
		return 4
	}
	return 1
}

// Resolve picks the level and native name for one worker. The level is never
// above the effective lead mode and the native name never above the level,
// except that a Claude worker at level plan starts in default, which the
// gate holds to plan (launchNative).
func Resolve(agent string, r Request, c Ceiling, sandbox string) Resolved {
	level := c.Effective
	if sandbox == "read-only" {
		level = Plan
	}
	if r.Native == "" {
		return Resolved{Level: level, Native: Default(agent, level, sandbox), Source: "default"}
	}
	want := Parse(agent, r.Native)
	rank, ok := Rank(agent, r.Native)
	level = Lower(want, level)
	native, by := r.Native, ""
	switch {
	case !ok:
		native, by = Default(agent, level, sandbox), "not offered"
	case want != level || rank > loosestRank(agent, level, c.Auto):
		native = Default(agent, level, sandbox)
		switch {
		case sandbox == "read-only":
			by = "role sandbox"
		case Lower(c.Effective, c.Lead) != c.Lead && Lower(want, c.Effective) != want:
			by = "duolog mode"
		default:
			by = "lead " + string(c.Lead)
		}
	}
	// An entry may still name Claude plan to lower a worker to level plan;
	// the worker then starts in default (launchNative), which is also
	// Default's name for plan, so it is never looser.
	native = launchNative(agent, native)
	nativeRank, _ := Rank(agent, native)
	defaultRank, _ := Rank(agent, Default(agent, level, sandbox))
	return Resolved{Level: level, Native: native, Source: r.Source, LoweredBy: by, Looser: nativeRank > defaultRank}
}

// LowerNative returns the lower-ranked of two names, ignoring empty ones. An
// unknown name counts as lowest and is returned; Resolve then gives the
// default.
func LowerNative(agent, a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	ra, oka := Rank(agent, a)
	rb, okb := Rank(agent, b)
	switch {
	case !oka:
		return a
	case !okb:
		return b
	case rb < ra:
		return b
	}
	return a
}
