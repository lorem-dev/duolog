package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Trust reads what Claude's settings say about duolog's gate.
type Trust struct{}

// Hooks reports the hook state of a Claude process whose environment is
// environ: the home folder, the Claude config directory, settings and plugins
// are the ones that environment selects, not duolog's own. Claude needs no
// hook approval, so the state is always trusted; Installed says whether the
// gate is in a settings file or the enabled plugin. An environment without a
// home folder is trusthome.ErrNoHome.
func (Trust) Hooks(root string, environ []string) (agent.TrustState, error) {
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return agent.TrustState{}, err
	}
	plugin := install.PluginEnabledIn(root, home, environ)
	state := agent.TrustState{Trusted: true, Installed: plugin}
	settingsGate := false
	for _, path := range install.ClaudeSettingsFilesIn(root, home, environ) {
		settingsGate = settingsGate || install.HasGate(path)
		if state.HooksOff == "" && hooksOff(path) {
			state.HooksOff = path
		}
	}
	state.Installed = state.Installed || settingsGate
	if plugin && !settingsGate && !install.PluginInstalledIn(root, home, environ) {
		state.Unproven = "the duolog Claude plugin is enabled but not installed; run duolog install"
	}
	return state, nil
}

// Folder is true: Claude needs no folder trust for its hooks to run.
func (Trust) Folder(string, []string) (bool, error) { return true, nil }

// GateProblem says why the duolog gate cannot be shown to run for Claude, or
// returns "" when it can.
func (Trust) GateProblem(s agent.TrustState, _ bool) string {
	if s.HooksOff != "" {
		return s.HooksOff + " sets disableAllHooks"
	}
	if !s.Installed {
		return "Claude has no duolog gate here"
	}
	return s.Unproven
}

// HeldGateProblem says why a Claude process whose environment is environ
// cannot be shown to run duolog's gate before each tool call, for a launch
// that only the gate keeps read-only; "" when it can. Every PreToolUse hook
// that names the gate, in the settings files and the enabled plugin, must be
// the command duolog install writes (install.HookGroup.GateProgram), run by
// gate, the duolog on the launch's PATH, and one of them must cover every
// tool. A hook that only names the gate, such as one in a shell comment, or
// that runs another or a missing duolog, proves nothing. Claude's managed
// settings count as well: their hooks always run, and when they set
// allowManagedHooksOnly or disableAllHooks, only their own gate does.
func (Trust) HeldGateProblem(root string, environ []string, gate string) string {
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return err.Error()
	}
	files, only, problem := heldHookFiles(root, home, environ)
	if problem != "" {
		return problem
	}
	proven := false
	for _, path := range files {
		for _, group := range install.ReadHooks(path)["PreToolUse"] {
			for i := range group.Hooks {
				if !group.IsGate(i) {
					continue
				}
				if problem := heldHookProblem(root, path, group, i, gate); problem != "" {
					return problem
				}
				proven = proven || group.CoversEveryTool()
			}
		}
	}
	if !proven && only != "" {
		return "Claude's managed settings in " + only + " let no hook but their own run, and no managed PreToolUse hook runs duolog's gate for every tool; whoever manages them must add it"
	}
	if !proven {
		return "no Claude PreToolUse hook runs duolog's gate for every tool; run duolog install"
	}
	return ""
}

// heldHookFiles lists the settings and hooks files whose hooks a Claude
// process whose environment is environ runs: the managed files, and unless
// they let only managed hooks run, the settings files and the enabled
// plugin's hooks. only is the managed file that lets only managed hooks run,
// "" when none does.
func heldHookFiles(root, home string, environ []string) (files []string, only, problem string) {
	managed := managedFiles(root, home, environ)
	if only, problem = managedHooksOnly(managed); problem != "" || only != "" {
		return managed, only, problem
	}
	files = append(slices.Clone(managed), install.ClaudeSettingsFilesIn(root, home, environ)...)
	if install.PluginEnabledIn(root, home, environ) {
		files = append(files, install.PluginGateFilesIn(root, home, environ)...)
	}
	return files, "", ""
}

// managedFiles lists the files of Claude's managed settings for a process
// whose environment is environ, as workerenv reads them; tests replace it.
var managedFiles = func(root, home string, environ []string) []string {
	var files []string
	for _, layer := range workerenv.SettingsLayers("claude", workerenv.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: environ}) {
		if layer.Managed && layer.Path != "" {
			files = append(files, layer.Path)
		}
	}
	return files
}

// managedHooksOnly returns the first managed file that sets
// allowManagedHooksOnly or disableAllHooks, "" when none does. A managed file
// that exists but cannot be read may set them, so it is a problem.
func managedHooksOnly(files []string) (only, problem string) {
	for _, path := range files {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "Claude's managed settings in " + path + " cannot be read, so the hooks Claude runs cannot be shown: " + err.Error()
		}
		var doc struct {
			Only bool `json:"allowManagedHooksOnly"`
			Off  bool `json:"disableAllHooks"`
		}
		if json.Unmarshal(data, &doc) == nil && (doc.Only || doc.Off) {
			return path, ""
		}
	}
	return "", ""
}

// heldHookProblem says why handler i of group, a hook in the file at path
// that names the gate, does not prove the gate runs; "" when it does. In
// duolog's own checkout at root, the dev hook that runs the checkout's dev
// launcher proves it too: the launcher runs this checkout's build, which is
// not the duolog on the worker PATH. Whatever its command, a hook Claude
// does not wait for, async or with a short timeout, proves nothing.
func heldHookProblem(root, path string, group install.HookGroup, i int, gate string) string {
	program, ok := group.GateProgram(i, "claude", "PreToolUse")
	why := "it is not the gate command duolog install writes"
	if launcher, dev := group.DevGateLauncher(i, "claude", "PreToolUse", root); dev {
		ok = install.IsDevLauncher(launcher)
		if !ok {
			why = launcher + " is not duolog's dev launcher"
		}
	} else if ok && !sameProgram(program, gate) {
		ok, why = false, "it runs "+program+", not the duolog on the worker PATH ("+gate+")"
	}
	if ok {
		why, ok = group.GateWaits(i)
	}
	if ok {
		return ""
	}
	h := group.Hooks[i]
	text := strings.Join(append([]string{h.Command}, h.Args...), " ")
	return fmt.Sprintf("run duolog install: the Claude PreToolUse hook in %s does not prove the gate runs, since %s (hook %q)", path, why, text)
}

// sameProgram reports whether a hook's program is gate: a bare name, which
// the hook's shell finds on the PATH where gate was found, or an absolute
// path to the same existing file.
func sameProgram(program, gate string) bool {
	if !strings.ContainsAny(program, `/\`) {
		return true
	}
	if !filepath.IsAbs(program) {
		return false
	}
	info, err := os.Stat(program)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if filepath.Clean(program) == filepath.Clean(gate) {
		return true
	}
	other, err := os.Stat(gate)
	return err == nil && os.SameFile(info, other)
}

// hooksOff reports whether the Claude settings file sets disableAllHooks. A
// missing or broken file does not.
func hooksOff(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		Off bool `json:"disableAllHooks"`
	}
	return json.Unmarshal(data, &doc) == nil && doc.Off
}

// ClaudeFolder reports whether Claude Code trusts the folder root, from the
// hasTrustDialogAccepted flag of root or one of its parents in Claude's user
// state under home. Only that flag is decoded; nothing else of the file is
// kept. known is false when the state is missing, unreadable or not JSON,
// and then trusted is false too.
func ClaudeFolder(home, root string) (trusted, known bool) {
	data, err := os.ReadFile(trusthome.ClaudeState(home))
	if err != nil {
		return false, false
	}
	var s struct {
		Projects map[string]struct {
			Accepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(data, &s) != nil {
		return false, false
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return false, false
	}
	accepted := map[string]bool{}
	for path, p := range s.Projects {
		if p.Accepted {
			accepted[pathutil.Clean(pathutil.Resolved(path))] = true
		}
	}
	dir := pathutil.Clean(pathutil.Resolved(root))
	for {
		if accepted[dir] {
			return true, true
		}
		parent := pathutil.Clean(filepath.Dir(dir))
		if parent == dir {
			return false, true
		}
		dir = parent
	}
}
