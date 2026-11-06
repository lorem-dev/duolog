package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
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
		for claude, codex := range view.Sync.Agents.ModelMap {
			if role.Use.Model == claude {
				add(codex)
			}
			if role.Use.Model == codex {
				add(claude)
			}
		}
		out[name] = list
	}
	return out
}

// layerMode is one project-layer entry; nil marks a field the layer leaves.
type layerMode struct {
	Claude        *string   `yaml:"claude"`
	Codex         *string   `yaml:"codex"`
	Network       *bool     `yaml:"network"`
	WritableRoots *[]string `yaml:"writable_roots"`
}

func (l layerMode) entry() config.WorkerMode {
	var e config.WorkerMode
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
	for _, agent := range []string{"claude", "codex"} {
		ptr := l.Claude
		if agent == "codex" {
			ptr = l.Codex
		}
		if ptr == nil || *ptr == "" {
			continue
		}
		rank, _ := mode.Rank(agent, *ptr)
		limit := config.DefaultCap
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
	if rank, _ := mode.Rank("claude", e.Claude); rank > config.DefaultCap {
		ignore("claude", e.Claude)
		e.Claude = ""
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

// refuseRoot compares canonical paths (pathutil.Canonical): symlinks in the
// existing prefix resolved, case folded where the volume ignores it.
func refuseRoot(root, home string, getenv func(string) string) string {
	canonical := pathutil.Canonical(root)
	if canonical == filepath.VolumeName(canonical)+string(filepath.Separator) {
		return "a volume root is too wide"
	}
	if pathutil.Within(canonical, pathutil.Canonical(home)) {
		return "the home folder and its ancestors are too wide"
	}
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return "a writable root must be a folder, not a file"
	}
	for _, p := range protectedFolders(runtime.GOOS, home, getenv) {
		if pathutil.Overlap(p, root) {
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

func cloneModes(m map[string]config.WorkerMode) map[string]config.WorkerMode {
	out := make(map[string]config.WorkerMode, len(m))
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
