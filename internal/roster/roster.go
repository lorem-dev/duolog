// Package roster formats compact routing hints without starting agents.
package roster

import (
	"sort"
	"strings"

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
		parts = append(parts, name+"="+models.Normalize(target.Agent, target.Model))
	}
	return strings.Join(parts, " ")
}
