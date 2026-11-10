package codex

import (
	"os"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/install"
)

// Installer writes duolog's hooks and skill for Codex: the hooks in
// .codex/hooks.json, the skill in .agents/skills, and the install offer in
// AGENTS.md.
type Installer struct {
	// SkillsDir is agents.codex.skills_dir: the project skills folder, with
	// slashes, below the project root. "" means DefaultSkillsDir.
	SkillsDir string
}

// HookFile is the hooks file whose hooks scope selects: user scope is the
// Codex home that home selects (CODEX_HOME applies), project scope .codex
// under base.
func (Installer) HookFile(scope, base, home string) string {
	if scope == "user" {
		environ := append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
		dir, err := kit.Home(environ, "CODEX_HOME", ".codex")
		if err != nil {
			dir = filepath.Join(home, ".codex")
		}
		return filepath.Join(dir, "hooks.json")
	}
	return filepath.Join(base, ".codex", "hooks.json")
}

// DefaultSkillsDir is the project skills folder of Codex when
// agents.codex.skills_dir is not set. User scope always keeps it under home.
const DefaultSkillsDir = ".agents/skills"

// SkillsDirOrDefault returns dir, or DefaultSkillsDir when dir is empty.
func SkillsDirOrDefault(dir string) string {
	if dir == "" {
		return DefaultSkillsDir
	}
	return dir
}

// WithSkillsDir returns i with the project skills folder dir, as
// agents.codex.skills_dir sets it.
func (i Installer) WithSkillsDir(dir string) Installer {
	i.SkillsDir = dir
	return i
}

// SkillDir is the folder the bundled skills go into: under home for user
// scope, and the configured folder under base for project scope.
func (i Installer) SkillDir(scope, base, _ string) string {
	if scope == "user" {
		return filepath.Join(base, filepath.FromSlash(DefaultSkillsDir))
	}
	return filepath.Join(base, filepath.FromSlash(SkillsDirOrDefault(i.SkillsDir)))
}

// DefaultSkillDir is the default project skills folder when the configured one
// differs from it, and "" otherwise. An earlier install may have put duolog's
// skills there.
func (i Installer) DefaultSkillDir(scope, base, _ string) string {
	if scope == "user" || SkillsDirOrDefault(i.SkillsDir) == DefaultSkillsDir {
		return ""
	}
	return filepath.Join(base, filepath.FromSlash(DefaultSkillsDir))
}

// Hooks merges duolog's Codex hooks, with gate timeouts of timeout seconds,
// into the hooks file whose bytes are existing (nil for none). A timeout of
// zero removes duolog's hooks instead. The notes are the steps the person
// still has to do.
func (Installer) Hooks(existing []byte, timeout int) ([]byte, []string, error) {
	var ours []byte
	var notes []string
	if timeout != 0 {
		var err error
		if ours, err = HooksJSON(timeout); err != nil {
			return nil, nil, err
		}
		notes = []string{"Codex: approve the duolog hooks in /hooks (press t), and trust this project, or Codex will not run them."}
	}
	data, err := install.Merge(existing, ours)
	return data, notes, err
}

// OurHooks returns duolog's own hooks in the hooks file whose bytes are data,
// as install.OurHooks reads them.
func (Installer) OurHooks(data []byte) (map[string][]string, error) { return install.OurHooks(data) }

// DevHooks reports whether duolog's hooks in data run the dev launcher of the
// duolog checkout at root.
func (Installer) DevHooks(data []byte, root string) bool { return install.RunsDevLauncher(data, root) }

// InstructionFiles names the instruction file Codex reads in the project at
// root, AGENTS.md, and every place one could have been written.
func (Installer) InstructionFiles(root string) (current string, all []string) {
	path := filepath.Join(root, "AGENTS.md")
	return path, []string{path}
}

// GateChanged reports whether the gate command differs between two bytes of
// a hooks file. Codex skips a hook whose command changed after approval, and
// its trusted_hash stays, so trust must not count that hash for the new
// command.
func (Installer) GateChanged(old, updated []byte) bool {
	return install.GateCommands(old) != install.GateCommands(updated)
}

// MarkStale records the approvals that a changed gate command made stale in
// the hooks file at path, under home.
func (Installer) MarkStale(home, path string) error {
	return install.MarkStaleApprovals(home, path)
}

// HooksJSON renders the hooks document of Codex, as the plugin ships it.
func HooksJSON(timeout int) ([]byte, error) {
	return install.Document(events(timeout))
}

// events returns the hook entries of Codex in a fixed order. Timeouts are in
// seconds.
func events(timeout int) []install.Event { return eventsWith(timeout, command) }

// devEvents are the hook entries of duolog's own repository, which run the
// dev launcher of the checkout (docs/contributing.md) instead of duolog on
// PATH. duolog install never writes them.
func devEvents(timeout int) []install.Event { return eventsWith(timeout, devCommand) }

// eventsWith returns the hook entries of Codex, each command written by cmd.
func eventsWith(timeout int, cmd func(goos, args string, gate bool) string) []install.Event {
	handler := func(args string, gate bool) install.Handler {
		return install.Handler{Type: "command", Command: cmd("linux", args, gate), CommandWindows: cmd("windows", args, gate)}
	}
	hook := func(name string) install.Group {
		return install.Group{Hooks: []install.Handler{handler("hook "+name+" --agent codex", false)}}
	}
	gate := func(name string) install.Group {
		h := handler("gate --agent codex --event "+name, true)
		h.Timeout = timeout
		return install.Group{Hooks: []install.Handler{h}}
	}
	return []install.Event{
		{Name: "SessionStart", Groups: []install.Group{hook("SessionStart")}},
		{Name: "SessionEnd", Groups: []install.Group{hook("SessionEnd")}},
		{Name: "UserPromptSubmit", Groups: []install.Group{hook("UserPromptSubmit")}},
		{Name: "PostToolUse", Groups: []install.Group{hook("PostToolUse")}},
		// A peer's turn end; a lead's Stop does nothing.
		{Name: "Stop", Groups: []install.Group{hook("Stop")}},
		{Name: "PreToolUse", Groups: []install.Group{gate("PreToolUse")}},
		{Name: "PermissionRequest", Groups: []install.Group{gate("PermissionRequest")}},
	}
}

// CodexGateCommands returns the POSIX and Windows commands duolog installs for
// the Codex gate on event. Codex runs a hook only while its command is the one
// the person approved, so trust compares an approved gate with these.
func CodexGateCommands(event string) (posix, windows string) {
	args := "gate --agent codex --event " + event
	return command("linux", args, true), command("windows", args, true)
}

// devCommand is the dev launcher form of a duolog hook command, as the
// .codex/hooks.json of duolog's own repository writes it.
func devCommand(goos, args string, gate bool) string {
	if goos == "windows" {
		return "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/dev/bin/duolog.ps1 " + args
	}
	s := `r=$(git rev-parse --show-toplevel 2>/dev/null); if [ -x "$r/scripts/dev/bin/duolog" ]; then exec "$r"/scripts/dev/bin/duolog ` + args + "; "
	if gate {
		s += `elif [ "${DUOLOG_DEPTH:-0}" -ge 1 ] 2>/dev/null; then exit 2; `
	}
	return s + "fi"
}

// ownCommands reports whether the POSIX and Windows commands are those of a
// duolog hook for event: the installed form, or with dev true also the dev
// launcher form.
func ownCommands(event, posix, windows string, dev bool) bool {
	forms := [][]install.Event{events(install.GateTimeout)}
	if dev {
		forms = append(forms, devEvents(install.GateTimeout))
	}
	for _, es := range forms {
		for _, e := range es {
			if e.Name != event {
				continue
			}
			for _, g := range e.Groups {
				for _, h := range g.Hooks {
					if posix == h.Command && windows == h.CommandWindows {
						return true
					}
				}
			}
		}
	}
	return false
}

// Codex carries both shell guards in one shared file. POSIX must exec so
// registration records the agent, not a short-lived shell.
func command(goos, args string, gate bool) string {
	if goos == "windows" {
		body := powershellGuard(args, gate)
		// No variable expansion or embedded double quotes: safe when the outer
		// Windows shell is PowerShell, Git Bash or cmd. Codex documents no shell.
		return `powershell -NoProfile -NonInteractive -Command "` + body + `"`
	}
	s := "if command -v duolog >/dev/null 2>&1; then exec duolog " + args + "; "
	if gate {
		s += `elif [ "${DUOLOG_DEPTH:-0}" -ge 1 ] 2>/dev/null; then exit 2; `
	}
	return s + "fi"
}

// powershellGuard finds duolog with where.exe and reads the exit code without a
// module cmdlet. Measured on hosted Windows runners, x64 and ARM: PowerShell
// starts in 0.2 seconds, but Get-Command for a missing program searches every
// module for 24 seconds, and Get-Variable took 18 to 30 seconds in a worker
// environment without PSModulePath. Every Codex tool call runs this guard.
// [char]36 is the dollar sign, which an outer shell would otherwise expand, and
// $PATH:duolog keeps where.exe out of the current directory.
func powershellGuard(args string, gate bool) string {
	lastExit := "[int][scriptblock]::Create('' + [char]36 + 'LASTEXITCODE').Invoke()[0]"
	s := "& ([Environment]::SystemDirectory + '\\where.exe') /Q ([char]36 + 'PATH:duolog'); if (" + lastExit + " -eq 0) { duolog " + args + "; exit " + lastExit + " }; "
	if gate {
		s += "if ([Environment]::GetEnvironmentVariable('DUOLOG_DEPTH') -match '^0*[1-9][0-9]*$') { exit 2 }; "
	}
	return s + "exit 0"
}
