// Package mode maps native permissions to the lead's four permission levels.
package mode

// Mode orders the permissions available to a worker, from least to most.
type Mode string

const (
	Plan  Mode = "plan"
	Ask   Mode = "ask"
	Edits Mode = "edits"
	Full  Mode = "full"
)

// Parse translates native names. Unknown permissions never grant full access.
func Parse(agent, raw string) Mode {
	switch agent {
	case "claude":
		switch raw {
		case "plan":
			return Plan
		case "default", "ask":
			return Ask
		case "auto", "acceptEdits", "edits":
			return Edits
		case "bypassPermissions", "full":
			return Full
		}
	case "codex":
		switch raw {
		case "read-only", "plan":
			return Plan
		case "default", "on-request", "ask":
			return Ask
		case "workspace-write", "edits":
			return Edits
		case "danger-full-access", "full":
			return Full
		}
	}
	return Ask
}

func level(m Mode) int {
	switch m {
	case Plan:
		return 0
	case Edits:
		return 2
	case Full:
		return 3
	default:
		return 1
	}
}

// Lower returns the narrower mode, treating unknown values as ask.
func Lower(a, b Mode) Mode {
	levels := []Mode{Plan, Ask, Edits, Full}
	return levels[min(level(a), level(b))]
}

// Args selects native flags within the role sandbox. Full mode uses the same
// flags as edits; automatic hook confirmation belongs to the policy layer.
func Args(agent string, m Mode, sandbox string) []string {
	if sandbox == "read-only" {
		m = Plan
	}
	if agent == "codex" {
		value := "workspace-write"
		if m == Plan {
			value = "read-only"
		}
		return []string{"-s", value}
	}
	if agent == "claude" {
		value := "default"
		switch m {
		case Plan:
			value = "plan"
		case Edits, Full:
			value = "acceptEdits"
		}
		return []string{"--permission-mode", value}
	}
	return nil
}
