package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/mode"
)

// WorkerModes is the native permission mode a delegate worker starts in, per
// role and per model family. Absent entries mean duolog's own default flags.
type WorkerModes struct {
	Roles    map[string]WorkerMode `yaml:"roles" jsonschema_description:"Native mode per role; user level, project layers may only lower. Absent means duolog's default flags."`
	Families map[string]WorkerMode `yaml:"families" jsonschema_description:"Native mode per model family, such as luna or haiku; combined with the role entry by taking the lower."`
	// Ignored names each project-layer value that Load dropped because it was
	// above the user layer.
	Ignored []string `yaml:"-" jsonschema:"-"`
}

// WorkerMode is one role or family entry.
type WorkerMode struct {
	Claude        string   `yaml:"claude" jsonschema_description:"Native Claude permission mode a worker starts in; it never exceeds the lead's mode. Absent means duolog's default." jsonschema:"enum=plan,enum=default,enum=acceptEdits,enum=auto,enum=bypassPermissions"`
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

// Request combines the role and family entries for one agent by taking the
// lower. Source names only the entries that set a value.
func (w WorkerModes) Request(role, family, agent string) mode.Request {
	r, f := w.Roles[role].For(agent), w.Families[family].For(agent)
	var sources []string
	if r != "" {
		sources = append(sources, "role "+role)
	}
	if f != "" {
		sources = append(sources, "family "+family)
	}
	if len(sources) == 0 {
		return mode.Request{}
	}
	return mode.Request{Native: mode.LowerNative(agent, r, f), Source: strings.Join(sources, ", ")}
}

// Extras returns the role entry's Codex network switch and writable roots,
// already expanded and cleaned by Load.
func (w WorkerModes) Extras(role string) (network bool, roots []string) {
	e := w.Roles[role]
	return e.Network, e.WritableRoots
}

// Family names the model family of an alias or version pin.
func Family(cfg *Config, model string) string {
	if m, ok := cfg.Models[model]; ok {
		return cmp.Or(m.Family, model)
	}
	parts := strings.Split(model, "-")
	switch {
	case strings.HasPrefix(model, "gpt-"):
		return parts[len(parts)-1]
	case strings.HasPrefix(model, "claude-") && len(parts) > 1:
		return parts[1]
	}
	return model
}

// layerMode is one project-layer entry; nil marks a field the layer leaves.
type layerMode struct {
	Claude        *string   `yaml:"claude"`
	Codex         *string   `yaml:"codex"`
	Network       *bool     `yaml:"network"`
	WritableRoots *[]string `yaml:"writable_roots"`
}

func (l layerMode) entry() WorkerMode {
	var e WorkerMode
	if l.Claude != nil {
		e.Claude = *l.Claude
	}
	if l.Codex != nil {
		e.Codex = *l.Codex
	}
	if l.Network != nil {
		e.Network = *l.Network
	}
	if l.WritableRoots != nil {
		e.WritableRoots = *l.WritableRoots
	}
	return e
}

type layerWorkerModes struct {
	WorkerModes struct {
		Roles    map[string]layerMode `yaml:"roles"`
		Families map[string]layerMode `yaml:"families"`
	} `yaml:"worker_modes"`
}

// resolveWorkerModes rebuilds cfg.WorkerModes from the layers: the user layer
// sets it, and the shared and local project layers may only lower it. Each
// layer is its bytes, nil when the file does not exist.
func resolveWorkerModes(cfg *Config, user, shared, local []byte) error {
	var base struct {
		WorkerModes WorkerModes `yaml:"worker_modes"`
	}
	if err := yaml.Unmarshal(user, &base); err != nil {
		return err
	}
	baseRoles, baseFams := base.WorkerModes.Roles, base.WorkerModes.Families
	for _, kind := range []string{"roles", "families"} {
		entries := baseRoles
		if kind == "families" {
			entries = baseFams
		}
		for _, name := range sortedKeys(entries) {
			e := entries[name]
			if err := validateWorkerEntry(cfg, kind, name, e); err != nil {
				return err
			}
			if kind == "roles" {
				roots, err := expandRoots(name, e.WritableRoots)
				if err != nil {
					return err
				}
				e.WritableRoots = roots
				entries[name] = e
			}
		}
	}
	out := WorkerModes{Roles: cloneModes(baseRoles), Families: cloneModes(baseFams)}
	for i, data := range [][]byte{shared, local} {
		file := ".duolog/config.yaml"
		if i == 1 {
			file = ".duolog/config.local.yaml"
		}
		var layer layerWorkerModes
		if err := yaml.Unmarshal(data, &layer); err != nil {
			return err
		}
		for _, kind := range []string{"roles", "families"} {
			layerEntries, baseEntries, outEntries := layer.WorkerModes.Roles, baseRoles, out.Roles
			if kind == "families" {
				layerEntries, baseEntries, outEntries = layer.WorkerModes.Families, baseFams, out.Families
			}
			for _, name := range sortedKeys(layerEntries) {
				l := layerEntries[name]
				if err := validateWorkerEntry(cfg, kind, name, l.entry()); err != nil {
					return fmt.Errorf("%s: %w", file, err)
				}
				if err := lowerWorkerEntry(&out, outEntries, baseEntries, kind, name, file, l); err != nil {
					return fmt.Errorf("%s: %w", file, err)
				}
			}
		}
	}
	for _, entries := range []map[string]WorkerMode{out.Roles, out.Families} {
		for name, e := range entries {
			if e.Claude == "" && e.Codex == "" && !e.Network && len(e.WritableRoots) == 0 {
				delete(entries, name)
			}
		}
	}
	if len(out.Roles) == 0 {
		out.Roles = nil
	}
	if len(out.Families) == 0 {
		out.Families = nil
	}
	cfg.WorkerModes = out
	return nil
}

// lowerWorkerEntry applies one project-layer entry to out, keeping a value
// only when it is at or below the user layer's, or below duolog's default
// when the user layer has none.
func lowerWorkerEntry(out *WorkerModes, outEntries, baseEntries map[string]WorkerMode, kind, name, file string, l layerMode) error {
	base, hasBase := baseEntries[name]
	e := outEntries[name]
	path := "worker_modes." + kind + "." + name
	applies := func(value string) string {
		if value == "" {
			return "duolog's default"
		}
		return value
	}
	ignore := func(field, value, applying string) {
		out.Ignored = append(out.Ignored, fmt.Sprintf(
			"%s.%s %s in %s is ignored: a project config may only lower worker modes; %s applies",
			path, field, value, file, applies(applying)))
	}
	for _, agent := range []string{"claude", "codex"} {
		ptr := l.Claude
		if agent == "codex" {
			ptr = l.Codex
		}
		if ptr == nil || *ptr == "" {
			continue
		}
		rank, _ := mode.Rank(agent, *ptr)
		limit := 2
		if b := base.For(agent); b != "" {
			limit, _ = mode.Rank(agent, b)
		}
		if rank > limit {
			ignore(agent, *ptr, base.For(agent))
			continue
		}
		if agent == "claude" {
			e.Claude = *ptr
		} else {
			e.Codex = *ptr
		}
	}
	if l.Network != nil {
		switch {
		case !*l.Network:
			e.Network = false
		case hasBase && base.Network:
			e.Network = true
		default:
			baseValue := ""
			if hasBase {
				baseValue = "false"
			}
			ignore("network", "true", baseValue)
		}
	}
	if l.WritableRoots != nil {
		roots, err := expandRoots(name, *l.WritableRoots)
		if err != nil {
			return err
		}
		kept := []string{}
		for i, root := range roots {
			if slices.Contains(base.WritableRoots, root) {
				kept = append(kept, root)
				continue
			}
			baseValue := strings.Join(base.WritableRoots, ", ")
			ignore("writable_roots", (*l.WritableRoots)[i], baseValue)
		}
		e.WritableRoots = kept
	}
	outEntries[name] = e
	return nil
}

func validateWorkerEntry(cfg *Config, kind, name string, e WorkerMode) error {
	path := "worker_modes." + kind + "." + name
	var role Role
	if kind == "roles" {
		var ok bool
		if role, ok = cfg.Roles[name]; !ok {
			return fmt.Errorf("%s: no such role", path)
		}
	}
	for _, agent := range []string{"claude", "codex"} {
		native := e.For(agent)
		if native == "" {
			continue
		}
		if agent == "codex" && native == "danger-full-access" {
			return fmt.Errorf("%s.codex: danger-full-access is not offered to workers; use network and writable_roots", path)
		}
		if _, ok := mode.Rank(agent, native); !ok {
			return fmt.Errorf("%s.%s: unknown native mode %q", path, agent, native)
		}
	}
	if !e.Network && len(e.WritableRoots) == 0 {
		return nil
	}
	switch {
	case kind == "families":
		return fmt.Errorf("%s: network and writable_roots belong to a role", path)
	case role.Sandbox != "workspace-write":
		return fmt.Errorf("%s: network and writable_roots need a workspace-write role", path)
	case e.Codex != "" && e.Codex != "workspace-write":
		return fmt.Errorf("%s: network and writable_roots need codex workspace-write", path)
	}
	return nil
}

// expandRoots expands, cleans and checks the writable roots of one role.
func expandRoots(role string, roots []string) ([]string, error) {
	if len(roots) == 0 {
		return roots, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("worker_modes.roles.%s.writable_roots: home folder: %w", role, err)
	}
	home = filepath.Clean(home)
	out := make([]string, 0, len(roots))
	for i, raw := range roots {
		root, reason := expandRoot(raw, home)
		if reason == "" {
			reason = refuseRoot(root, home)
		}
		if reason != "" {
			return nil, fmt.Errorf("worker_modes.roles.%s.writable_roots[%d] %s: %s", role, i, raw, reason)
		}
		out = append(out, root)
	}
	return out, nil
}

func expandRoot(raw, home string) (string, string) {
	p := raw
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		return "", "must be an absolute folder or start with ~/"
	}
	return filepath.Clean(p), ""
}

func refuseRoot(root, home string) string {
	if root == filepath.VolumeName(root)+string(filepath.Separator) {
		return "a volume root is too wide"
	}
	if within(root, home) {
		return "the home folder and its ancestors are too wide"
	}
	userDir, _ := os.UserConfigDir()
	protected := []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".claude"),
		filepath.Join(home, "Library", "Keychains"),
	}
	for _, env := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
			protected = append(protected, filepath.Clean(v))
		}
	}
	if userDir != "" {
		protected = append(protected, filepath.Join(userDir, "duolog"))
	}
	for _, p := range protected {
		if within(p, root) || within(root, p) {
			return "protected: it holds credentials or duolog's own settings"
		}
	}
	return ""
}

// within reports whether child is parent or lies inside it.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cloneModes(m map[string]WorkerMode) map[string]WorkerMode {
	out := make(map[string]WorkerMode, len(m))
	for k, v := range m {
		v.WritableRoots = slices.Clone(v.WritableRoots)
		out[k] = v
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
