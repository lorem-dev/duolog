package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
)

var versionPattern = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

func newer(a, b string) bool {
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(aa) && i < len(bb); i++ {
		x, _ := strconv.Atoi(aa[i])
		y, _ := strconv.Atoi(bb[i])
		if x != y {
			return x > y
		}
	}
	return false
}

type catalogEntry struct {
	models.Entry
	Levels []effortLevel `json:"supported_reasoning_levels"`
}

// Real `codex debug models` gives objects with an "effort" key and a
// description. A bare string is also accepted.
type effortLevel struct{ Effort string }

func (level *effortLevel) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &level.Effort)
	}
	var entry struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return err
	}
	level.Effort = entry.Effort
	return nil
}
func readCatalog(ctx context.Context) ([]catalogEntry, error) {
	data, err := metadata(ctx, "codex", "debug", "models")
	if err != nil {
		return nil, err
	}
	var result struct {
		Models []catalogEntry `json:"models"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result.Models == nil {
		return nil, fmt.Errorf("missing models list")
	}
	return result.Models, nil
}
func checkModels(cfg *config.Config, entries []catalogEntry) []Finding {
	var out []Finding
	add := func(message string) { out = append(out, Finding{"warning", message}) }
	list := make([]models.Entry, 0, len(entries))
	byName := make(map[string]catalogEntry)
	for _, entry := range entries {
		list = append(list, entry.Entry)
		byName[entry.Slug] = entry
	}
	aliases := make([]string, 0, len(cfg.Models))
	for alias := range cfg.Models {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	roles := make([]string, 0, len(cfg.Roles))
	for role := range cfg.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, alias := range aliases {
		model := cfg.Models[alias]
		if model.Agent != "codex" {
			continue
		}
		if _, ok := models.Latest(model.Family, list); ok {
			continue
		}
		var users []string
		for _, name := range roles {
			role := cfg.Roles[name]
			if (role.Use.Agent == "codex" && role.Use.Model == alias) || (role.Fallback.Agent == "codex" && role.Fallback.Model == alias) {
				users = append(users, name)
			}
		}
		add(fmt.Sprintf("Alias %s has no visible model; roles: %s", alias, strings.Join(users, ", ")))
	}
	for _, name := range roles {
		role := cfg.Roles[name]
		for _, target := range []config.Target{role.Use, role.Fallback} {
			if target.Agent != "codex" {
				continue
			}
			resolved := target.Model
			if model, ok := cfg.Models[target.Model]; ok {
				var found bool
				resolved, found = models.Latest(model.Family, list)
				if !found {
					continue
				}
			} else {
				family := models.Normalize("codex", target.Model)
				latest, ok := models.Latest(family, list)
				if ok && latest != target.Model && models.Normalize("codex", latest) == family {
					versions := []models.Entry{{Slug: target.Model}, {Slug: latest}}
					best, _ := models.Latest(family, versions)
					if best != target.Model {
						add(fmt.Sprintf("Role %s pins %s; newer model %s exists", name, target.Model, latest))
					}
				}
			}
			entry, ok := byName[resolved]
			if !ok || entry.Visibility == "hide" {
				add(fmt.Sprintf("Role %s model %s is absent from the visible catalog", name, resolved))
				continue
			}
			if target.Effort == "" {
				continue
			}
			supported := false
			for _, level := range entry.Levels {
				if level.Effort == target.Effort {
					supported = true
				}
			}
			if !supported {
				add(fmt.Sprintf("Role %s model %s does not support effort %s", name, resolved, target.Effort))
			}
		}
	}
	return out
}
