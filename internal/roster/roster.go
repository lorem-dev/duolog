// Package roster formats compact routing hints without starting agents.
package roster

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/session"
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
		parts = append(parts, name+"="+models.Normalize(target.Agent, target.Model)+"/"+target.Effort)
	}
	return strings.Join(parts, " ")
}

// CapsNote says what the other agent lacks compared with the lead's agent and
// whether it has duolog hooks. It is empty when there is nothing to say; an
// unknown inventory is never reported as a difference.
func CapsNote(lead, other capability.Inventory, otherHasDuolog bool) string {
	var parts []string
	if !lead.Unknown && !other.Unknown {
		if n := len(capability.Diff(lead, other)); n > 0 {
			parts = append(parts, fmt.Sprintf("%s lacks %d", other.Agent, n))
		}
	}
	if !otherHasDuolog {
		parts = append(parts, other.Agent+" has no duolog hooks: sandbox only")
	}
	return strings.Join(parts, "; ")
}

// StartNote uses persisted capability lists only; SessionStart never scans.
func StartNote(root string, lead session.Lead) string {
	other := "codex"
	if lead.Agent == "codex" {
		other = "claude"
	}
	mine, theirs := capability.Cached(lead.Agent, root), capability.Cached(other, root)
	count := "unknown"
	if !mine.Unknown && !theirs.Unknown {
		count = fmt.Sprint(len(capability.Diff(mine, theirs)))
	}
	note := "; caps=" + count
	if !capability.HasDuolog(other, root) {
		note += "; " + other + " has no duolog hooks: sandbox only"
	}
	return note
}
