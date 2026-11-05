package mode

// Rank orders the native names duolog offers a worker. Claude: plan 0,
// default 1, acceptEdits 2, auto 3, bypassPermissions 4. Codex: read-only 0,
// workspace-write 2. Every other name is not offered: (-1, false).
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
// mode, and full uses the same value as edits.
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
		case Plan:
			return "plan"
		case Edits, Full:
			return "acceptEdits"
		}
		return "default"
	}
	return ""
}

// Flags turns a native name into the agent's command-line flags.
func Flags(agent, native string) []string {
	switch agent {
	case "codex":
		return []string{"-s", native}
	case "claude":
		return []string{"--permission-mode", native}
	}
	return nil
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
// above the effective lead mode and the native name never above the level.
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
