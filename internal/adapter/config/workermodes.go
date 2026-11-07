package config

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// routeModels lists the models of each role's route in view: use, fallback,
// and the use model mapped to the other agent by view's model_map.
func routeModels(view userView) map[string][]string {
	out := make(map[string][]string, len(view.Roles))
	for name, role := range view.Roles {
		var list []string
		add := func(m string) {
			if m != "" && !slices.Contains(list, m) {
				list = append(list, m)
			}
		}
		add(role.Use.Model)
		add(role.Fallback.Model)
		for _, row := range view.Sync.Agents.Rows() {
			if !slices.Contains(slices.Collect(maps.Values(row)), role.Use.Model) {
				continue
			}
			for _, agent := range slices.Sorted(maps.Keys(row)) {
				if row[agent] != role.Use.Model {
					add(row[agent])
				}
			}
		}
		out[name] = list
	}
	return out
}

// layerMode is one project-layer entry; nil marks a field the layer leaves.
type layerMode struct {
	// Native is the layer's native name by agent name.
	Native        map[string]string
	Network       *bool     `yaml:"network"`
	WritableRoots *[]string `yaml:"writable_roots"`
}

// UnmarshalYAML reads network, writable_roots and one native name per agent.
func (l *layerMode) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: a worker mode must be a mapping", node.Line)
	}
	seen := map[string]int{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if first, dup := seen[key]; dup {
			return fmt.Errorf("line %d: mapping key %q already defined at line %d", node.Content[i].Line, key, first)
		}
		seen[key] = node.Content[i].Line
		var err error
		switch key {
		case "network":
			err = value.Decode(&l.Network)
		case "writable_roots":
			err = value.Decode(&l.WritableRoots)
		default:
			var name string
			if err = value.Decode(&name); err == nil {
				if l.Native == nil {
					l.Native = map[string]string{}
				}
				l.Native[key] = name
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (l layerMode) entry() config.WorkerMode {
	var e config.WorkerMode
	for agent, name := range l.Native {
		e = e.With(agent, name)
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
	// Roles holds only the names: any change to a role caps its entry.
	Roles map[string]yaml.Node `yaml:"roles"`
}

// userView is the roles and models as the defaults and the user layer define
// them, before any project layer.
type userView struct {
	Roles  map[string]config.Role  `yaml:"roles"`
	Models map[string]config.Model `yaml:"models"`
	Sync   config.Sync             `yaml:"sync"`
}

func decodeUserView(merged *yaml.Node) (userView, error) {
	var v userView
	if merged.Kind == 0 {
		return v, nil
	}
	if err := merged.Decode(&v); err != nil {
		return v, fmt.Errorf("user config: %w", err)
	}
	return v, nil
}

// resolveWorkerModes rebuilds cfg.WorkerModes from the layers: the user layer
// sets it, and the shared and local project layers may only lower it. Each
// layer is its bytes, nil when the file does not exist. view is the roles and
// models before the project layers.
//
// The rule that keeps a project from raising a worker: a native name above
// duolog's default (and any Codex extra) comes only from a user-layer role
// entry, for a role that no project layer changes. Each project layer may
// only lower the entry as the layers before it left it. A family entry only
// lowers (Request), and it applies by the model's family in the user layer as
// well as in the merged config (Families).
func resolveWorkerModes(cfg *config.Config, view userView, user, shared, local []byte) error {
	var base struct {
		WorkerModes config.WorkerModes `yaml:"worker_modes"`
	}
	if err := yaml.Unmarshal(user, &base); err != nil {
		return err
	}
	userCfg := &config.Config{Roles: view.Roles}
	baseRoles, baseFams := base.WorkerModes.Roles, base.WorkerModes.Families
	for _, kind := range []string{"roles", "families"} {
		entries := baseRoles
		if kind == "families" {
			entries = baseFams
		}
		for _, name := range sortedKeys(entries) {
			e := entries[name]
			// User entries are checked against the user's own roles, so that a
			// project layer cannot make them fail to load.
			if err := validateWorkerEntry(userCfg, kind, name, e); err != nil {
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
	out := config.WorkerModes{Roles: cloneModes(baseRoles), Families: cloneModes(baseFams)}
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
			layerEntries, outEntries := layer.WorkerModes.Roles, out.Roles
			if kind == "families" {
				layerEntries, outEntries = layer.WorkerModes.Families, out.Families
			}
			for _, name := range sortedKeys(layerEntries) {
				l := layerEntries[name]
				if err := validateWorkerEntry(cfg, kind, name, l.entry()); err != nil {
					return fmt.Errorf("%s: %w", file, err)
				}
				if err := lowerWorkerEntry(&out, outEntries, kind, name, file, l); err != nil {
					return fmt.Errorf("%s: %w", file, err)
				}
			}
		}
		for _, name := range sortedKeys(layer.Roles) {
			capChangedRole(&out, name, file)
		}
	}
	out.SetUserView(view.Models, routeModels(view))
	for _, entries := range []map[string]config.WorkerMode{out.Roles, out.Families} {
		for name, e := range entries {
			if !hasNative(e) && !e.Network && len(e.WritableRoots) == 0 {
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
// only when it is at or below the entry as the layers before it left it, or
// at or below duolog's default when they set none.
func lowerWorkerEntry(out *config.WorkerModes, outEntries map[string]config.WorkerMode, kind, name, file string, l layerMode) error {
	base, hasBase := outEntries[name]
	e := base
	e.WritableRoots = slices.Clone(base.WritableRoots)
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
	e.Native = maps.Clone(base.Native)
	for _, agent := range slices.Sorted(maps.Keys(l.Native)) {
		name := l.Native[agent]
		if name == "" {
			continue
		}
		rank, _ := mode.Rank(agent, name)
		limit := config.DefaultCap
		if b := base.For(agent); b != "" {
			limit, _ = mode.Rank(agent, b)
		}
		if rank > limit {
			ignore(agent, name, base.For(agent))
			continue
		}
		e = e.With(agent, name)
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

// capChangedRole drops what a role entry asks above duolog's default once a
// project layer changes the role: its route or sandbox may no longer be the
// one the person gave the entry for. Lowering values stay.
func capChangedRole(out *config.WorkerModes, name, file string) {
	e, ok := out.Roles[name]
	if !ok {
		return
	}
	ignore := func(field, value string) {
		out.Ignored = append(out.Ignored, fmt.Sprintf(
			"worker_modes.roles.%s.%s %s is ignored: %s changes role %s; duolog's default applies",
			name, field, value, file, name))
	}
	e.Native = maps.Clone(e.Native)
	for _, agent := range slices.Sorted(maps.Keys(e.Native)) {
		if rank, _ := mode.Rank(agent, e.Native[agent]); rank > config.DefaultCap {
			ignore(agent, e.Native[agent])
			e = e.With(agent, "")
		}
	}
	if e.Network {
		ignore("network", "true")
		e.Network = false
	}
	if len(e.WritableRoots) > 0 {
		ignore("writable_roots", strings.Join(e.WritableRoots, ", "))
		e.WritableRoots = nil
	}
	out.Roles[name] = e
}

func validateWorkerEntry(cfg *config.Config, kind, name string, e config.WorkerMode) error {
	path := "worker_modes." + kind + "." + name
	var role config.Role
	if kind == "roles" {
		var ok bool
		if role, ok = cfg.Roles[name]; !ok {
			return fmt.Errorf("%s: no such role", path)
		}
	}
	for _, agent := range slices.Sorted(maps.Keys(e.Native)) {
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
	case e.For("codex") != "" && e.For("codex") != "workspace-write":
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
			reason = refuseRoot(root, home, os.Getenv)
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

// RefuseRoot says why a writable root is refused, or returns "" when it is
// not. delegate asks again at launch, when symlinks may point elsewhere.
func RefuseRoot(root string) string {
	return RefuseRootEnv(root, nil)
}

// RefuseRootEnv is RefuseRoot for a worker whose environment is environ: the
// home folder and the agent and credential folders that environment selects,
// such as its HOME, CLAUDE_CONFIG_DIR or CODEX_HOME, are protected as well as
// duolog's own. An environ without a home folder protects duolog's home only;
// a worker that relies on the gate is refused before then (delegate's gate
// preflight), as its agent's home cannot be read from environ.
func RefuseRootEnv(root string, environ []string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "home folder: " + err.Error()
	}
	if reason := refuseRoot(filepath.Clean(root), filepath.Clean(home), os.Getenv); reason != "" || environ == nil {
		return reason
	}
	getenv := func(name string) string { return trusthome.Getenv(environ, name) }
	if worker, err := trusthome.HomeIn(environ); err == nil && filepath.IsAbs(worker) {
		home = worker
	}
	return refuseRoot(filepath.Clean(root), filepath.Clean(home), getenv)
}

// RefuseRead says why a path that a task lists under Read, a file or a
// folder, is too wide to hand a worker's gate, as RefuseRoot says for a
// writable root, or returns "" when it is not.
func RefuseRead(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "home folder: " + err.Error()
	}
	root, home := filepath.Clean(path), filepath.Clean(home)
	if reason := refuseWide(root, home); reason != "" {
		return reason
	}
	return refuseProtected(root, home, os.Getenv)
}

// refuseRoot compares canonical paths (kit.Canonical): symlinks in the
// existing prefix resolved, case folded where the volume ignores it.
func refuseRoot(root, home string, getenv func(string) string) string {
	if reason := refuseWide(root, home); reason != "" {
		return reason
	}
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return "a writable root must be a folder, not a file"
	}
	return refuseProtected(root, home, getenv)
}

// refuseWide refuses a volume root, and the home folder and its ancestors.
func refuseWide(root, home string) string {
	canonical := kit.Canonical(root)
	if canonical == filepath.VolumeName(canonical)+string(filepath.Separator) {
		return "a volume root is too wide"
	}
	if kit.Within(canonical, kit.Canonical(home)) {
		return "the home folder and its ancestors are too wide"
	}
	return ""
}

// refuseProtected refuses a path that holds or lies in a folder of
// credentials or of agent and duolog settings.
func refuseProtected(root, home string, getenv func(string) string) string {
	for _, p := range protectedFolders(runtime.GOOS, home, getenv) {
		if kit.Overlap(p, root) {
			return "protected: it holds credentials or duolog's own settings"
		}
	}
	return ""
}

// protectedFolders lists the folders that hold credentials or agent and
// duolog settings, on goos, for the home folder.
func protectedFolders(goos, home string, getenv func(string) string) []string {
	join := filepath.Join
	protected := []string{
		join(home, ".ssh"),
		join(home, ".codex"),
		join(home, ".claude"),
		join(home, ".aws"),
		join(home, ".azure"),
		join(home, ".gnupg"),
		join(home, ".docker"),
		join(home, ".kube"),
		join(home, ".password-store"),
		join(home, ".config", "gh"),
		join(home, ".config", "gcloud"),
		join(home, ".local", "share", "keyrings"),
		join(home, "Library", "Keychains"),
		// Programs the lead runs from here: git hooks and fsmonitor, and
		// macOS login agents.
		join(home, ".config", "git"),
		join(home, "Library", "LaunchAgents"),
	}
	for _, env := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if v := getenv(env); v != "" && filepath.IsAbs(v) {
			protected = append(protected, filepath.Clean(v))
		}
	}
	if v := getenv("XDG_CONFIG_HOME"); v != "" && filepath.IsAbs(v) {
		protected = append(protected, join(v, "gh"), join(v, "gcloud"), join(v, "git"))
	}
	if goos == "windows" {
		// Credential Manager, the vault and the DPAPI master keys.
		roaming := cmp.Or(getenv("APPDATA"), join(home, "AppData", "Roaming"))
		local := cmp.Or(getenv("LOCALAPPDATA"), join(home, "AppData", "Local"))
		for _, base := range []string{roaming, local} {
			protected = append(protected, join(base, "Microsoft", "Credentials"), join(base, "Microsoft", "Vault"), join(base, "Microsoft", "Protect"))
		}
		protected = append(protected, join(roaming, "GitHub CLI"))
	}
	if userDir, err := os.UserConfigDir(); err == nil && userDir != "" {
		protected = append(protected, join(userDir, "duolog"))
	}
	return protected
}

// hasNative reports whether the entry sets a native name for any agent.
func hasNative(e config.WorkerMode) bool {
	return slices.ContainsFunc(slices.Collect(maps.Values(e.Native)), func(n string) bool { return n != "" })
}

func cloneModes(m map[string]config.WorkerMode) map[string]config.WorkerMode {
	out := make(map[string]config.WorkerMode, len(m))
	for k, v := range m {
		v.WritableRoots = slices.Clone(v.WritableRoots)
		v.Native = maps.Clone(v.Native)
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
