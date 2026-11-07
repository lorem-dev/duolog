// Package roster formats compact routing hints without starting agents.
package roster

import (
	"fmt"
	"os"
	"sort"
	"strings"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// Line uses aliases even when the configuration deliberately pins a version.
// lead identifies the session receiving the SessionStart hint.
func Line(cfg *config.Config, lead string, reg *session.Registry) string {
	parts := []string{"duolog"}
	if lead != "" {
		parts = append(parts, "lead="+lead)
	}
	if reg != nil {
		unavailable, err := reg.Unavailable()
		if err != nil {
			parts = append(parts, "availability=unknown")
		} else {
			for _, agent := range []string{"claude", "codex"} {
				if unavailable[agent] {
					parts = append(parts, agent+"=unavailable")
				}
			}
		}
	}
	names := make([]string, 0, len(cfg.Roles))
	for name := range cfg.Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := cfg.Roles[name].Use
		// A full model ID is a pin, and a pin must not read as a moving alias.
		model := route.Normalize(target.Agent, target.Model)
		if strings.HasPrefix(target.Model, "claude-") || strings.HasPrefix(target.Model, "gpt-") {
			model = target.Model
		}
		parts = append(parts, name+"="+model+"/"+target.Effort)
	}
	return strings.Join(parts, " ")
}

// Caps is what the roster asks of an agent's capabilities: the inventory its
// last scan cached, and whether duolog's hooks are in its settings.
type Caps interface {
	Cached(root string) caps.Inventory
	HasDuolog(root string, environ []string) bool
}

// Cached is the cached inventory of agent, or an unknown one for an agent
// without Caps.
func Cached(agents map[string]Caps, agent, root string) caps.Inventory {
	if c := agents[agent]; c != nil {
		return c.Cached(root)
	}
	return caps.Inventory{Agent: agent, Unknown: true}
}

// HasDuolog reports whether duolog's hooks are in agent's settings, read from
// duolog's own environment; an agent without Caps has none.
func HasDuolog(agents map[string]Caps, agent, root string) bool {
	c := agents[agent]
	return c != nil && c.HasDuolog(root, os.Environ())
}

// CapsNote says what the other agent lacks compared with the lead's agent and
// whether it has duolog hooks. It is empty when there is nothing to say; an
// unknown inventory is never reported as a difference.
func CapsNote(lead, other caps.Inventory, otherHasDuolog bool) string {
	var parts []string
	if !lead.Unknown && !other.Unknown {
		if n := len(caps.Diff(lead, other)); n > 0 {
			parts = append(parts, fmt.Sprintf("%s lacks %d", other.Agent, n))
		}
	}
	if !otherHasDuolog {
		parts = append(parts, other.Agent+" has no duolog hooks: sandbox only")
	}
	return strings.Join(parts, "; ")
}

// StartNote uses persisted capability lists only; SessionStart never scans.
// agents holds each registered agent's Caps by name.
func StartNote(root string, lead session.Lead, agents map[string]Caps) string {
	other := "codex"
	if lead.Agent == "codex" {
		other = "claude"
	}
	mine, theirs := Cached(agents, lead.Agent, root), Cached(agents, other, root)
	count := "unknown"
	if !mine.Unknown && !theirs.Unknown {
		count = fmt.Sprint(len(caps.Diff(mine, theirs)))
	}
	note := "; caps=" + count
	if !HasDuolog(agents, other, root) {
		note += "; " + other + " has no duolog hooks: sandbox only"
	}
	if cfg, err := configfile.Load(root); err == nil && cfg.Coauthors.Enabled {
		note += "; commits: add duolog coauthors"
	}
	return note
}

// LearningNote tells the lead to open tasks when learning mode is enabled.
func LearningNote(cfg *config.Config) string {
	if cfg.Learning.Enabled {
		return "; learning mode on: run duolog task start before a task"
	}
	return ""
}
