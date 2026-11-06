package config

import (
	"cmp"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// WorkerModes is the native permission mode a delegate worker starts in, per
// role and per model family. Absent entries mean duolog's own default flags.
type WorkerModes struct {
	Roles    map[string]WorkerMode `yaml:"roles" jsonschema_description:"Native mode per role; user level, project layers may only lower. Absent means duolog's default flags."`
	Families map[string]WorkerMode `yaml:"families" jsonschema_description:"Native mode per model family, such as luna or haiku; it only lowers: combined with the role entry by taking the lower; alone, a value above duolog's default changes nothing. Read-only roles ignore it."`
	// Ignored names each project-layer value that Load dropped because it was
	// above the layers before it, and each user value that Load dropped because
	// a project layer changes the role.
	Ignored []string `yaml:"-" jsonschema:"-"`
	// userModels is the models as the defaults and the user layer define them,
	// before any project layer.
	userModels map[string]Model
	// userRoutes is, per role, the models of its route as the defaults and the
	// user layer give it: use, fallback and the use model mapped to the other
	// agent by the user's model_map.
	userRoutes map[string][]string
}

// SetUserView records the models and the route models of each role as the
// defaults and the user layer give them, which Families reads.
// internal/adapter/config sets them when it loads the layers.
func (w *WorkerModes) SetUserView(models map[string]Model, routes map[string][]string) {
	w.userModels = models
	w.userRoutes = routes
}

// DefaultCap is the highest native name that is never looser than duolog's
// default flag at any level: Claude acceptEdits, Codex workspace-write.
const DefaultCap = 2

// WorkerMode is one role or family entry.
type WorkerMode struct {
	Claude        string   `yaml:"claude" jsonschema_description:"Native Claude permission mode a worker starts in; it never exceeds the lead's mode. plan sets the worker's level to plan: it starts in default, and the gate keeps it read-only. Absent means duolog's default." jsonschema:"enum=plan,enum=default,enum=acceptEdits,enum=auto,enum=bypassPermissions"`
	Codex         string   `yaml:"codex" jsonschema_description:"Native Codex sandbox a worker starts in; danger-full-access is not offered. Absent means duolog's default." jsonschema:"enum=read-only,enum=workspace-write"`
	Network       bool     `yaml:"network" jsonschema_description:"Codex workspace-write only, role entries only: lets the worker's commands reach the network; defaults to false."`
	WritableRoots []string `yaml:"writable_roots" jsonschema_description:"Codex workspace-write only, role entries only: absolute or ~/ folders the worker's commands may write besides the project; defaults to none."`
}

// For returns the entry's native name for an agent, empty when it sets none.
func (w WorkerMode) For(agent string) string {
	switch agent {
	case "claude":
		return w.Claude
	case "codex":
		return w.Codex
	}
	return ""
}

// Request combines the role entry and the entries of the given families for
// one agent by taking the lower. Only a role entry can ask for more than
// duolog's default: a family entry lowers, and without a role entry it asks
// at most acceptEdits or workspace-write. Without a role entry, a family entry
// above that is skipped, so it does not lower the level below what duolog's
// default gives. Source names the entries that set a value.
func (w WorkerModes) Request(role string, families []string, agent string) mode.Request {
	native := w.Roles[role].For(agent)
	var sources []string
	if native != "" {
		sources = append(sources, "role "+role)
	}
	for _, family := range families {
		f := w.Families[family].For(agent)
		if f == "" {
			continue
		}
		if rank, ok := mode.Rank(agent, f); native == "" && ok && rank > DefaultCap {
			continue
		}
		sources = append(sources, "family "+family)
		if native == "" {
			native = capName(agent)
		}
		native = mode.LowerNative(agent, native, f)
	}
	if len(sources) == 0 {
		return mode.Request{}
	}
	return mode.Request{Native: native, Source: strings.Join(sources, ", ")}
}

// capName is the native name of DefaultCap for an agent.
func capName(agent string) string {
	if agent == "codex" {
		return "workspace-write"
	}
	return "acceptEdits"
}

// Extras returns the role entry's Codex network switch and writable roots,
// already expanded and cleaned by Load.
func (w WorkerModes) Extras(role string) (network bool, roots []string) {
	e := w.Roles[role]
	return e.Network, e.WritableRoots
}

// Families names the families whose entries apply when role runs on model:
// the model's family in the merged config and as the user layer defines it,
// and the families of the role's route as the user layer gives it. Request
// takes the lowest of their entries, so a project source that moves the route
// (a learning ladder, routing.learned.yaml, a model_map, a version pin) or a
// model's family cannot escape a family entry that lowers the person's route.
func Families(cfg *Config, role, model string) []string {
	var families []string
	add := func(models map[string]Model, model string) {
		for _, f := range modelFamilies(models, model) {
			if !slices.Contains(families, f) {
				families = append(families, f)
			}
		}
	}
	user := cfg.WorkerModes.userModels
	add(cfg.Models, model)
	add(user, model)
	for _, m := range cfg.WorkerModes.userRoutes[role] {
		add(user, m)
	}
	return families
}

// RouteFamilies is Families for a route with the given sandbox. A read-only
// role starts read-only whatever a family entry says, so there the families
// are none: a family entry would only set the role's gate to plan, which
// denies its web reads. A role entry still does that.
func RouteFamilies(cfg *Config, role, model, sandbox string) []string {
	if sandbox == "read-only" {
		return nil
	}
	return Families(cfg, role, model)
}

// modelFamilies is the family of a model and, for a version pin, the family of
// every model alias named in the pin: claude-3-5-haiku-20241022 is haiku.
func modelFamilies(models map[string]Model, model string) []string {
	out := []string{familyOf(models, model)}
	if _, ok := models[model]; ok {
		return out
	}
	for _, part := range strings.Split(model, "-") {
		if _, ok := models[part]; ok {
			if f := familyOf(models, part); !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// Family names the model family of an alias or version pin.
func Family(cfg *Config, model string) string {
	return familyOf(cfg.Models, model)
}

// familyOf names the family of an alias or version pin. A pin's family is the
// first alias it names, so claude-3-5-haiku-20241022 is haiku; without one, a
// gpt pin's last part and a claude pin's second.
func familyOf(models map[string]Model, model string) string {
	if m, ok := models[model]; ok {
		return cmp.Or(m.Family, model)
	}
	parts := strings.Split(model, "-")
	if strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "claude-") {
		for _, part := range parts[1:] {
			if m, ok := models[part]; ok {
				return cmp.Or(m.Family, part)
			}
		}
	}
	switch {
	case strings.HasPrefix(model, "gpt-"):
		return parts[len(parts)-1]
	case strings.HasPrefix(model, "claude-") && len(parts) > 1:
		return parts[1]
	}
	return model
}
