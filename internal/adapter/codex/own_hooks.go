package codex

import (
	"regexp"
	"runtime"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
)

// Only literal arguments inside a complete generated guard are accepted. In
// particular, IsOurCommand alone also recognises paths for install migrations;
// that broader recognition must never authorise a worktree's program.
var guardedArgs = regexp.MustCompile(`duolog ((?:gate|hook)(?: [A-Za-z0-9_./:=@+-]+)*)`)

// ownCodexHandler reports whether a Codex handler with command posix and
// commandWindows windows is one duolog install writes for event: the current
// form or the dev launcher form exactly, or an older guard of a hook that is
// not the gate. Codex hooks take no args, so a handler with args is not
// duolog's. Each slot must hold its own OS's form, since Codex runs command
// on Unix and commandWindows on Windows; on Windows commandWindows must be
// set. An older gate guard without the DUOLOG_DEPTH branch lets a tool call
// through when duolog is not on PATH, so it does not count as the gate. The
// dev launcher form counts only with dev, where duolog's dev launcher is.
func ownCodexHandler(event, posix, windows string, args []string, dev bool) bool {
	if len(args) != 0 {
		return false
	}
	if ownCommands(event, posix, windows, dev) {
		return true
	}
	if runtime.GOOS == "windows" && windows == "" {
		return false
	}
	return olderOwn(event, posix, false) && (windows == "" || olderOwn(event, windows, true))
}

// olderOwn reports whether cmd, in the Windows slot or the POSIX one, is a
// guard of an older duolog install for event, other than the gate.
func olderOwn(event, cmd string, windowsSlot bool) bool {
	if !install.IsOurCommand(cmd) {
		return false
	}
	match := guardedArgs.FindStringSubmatch(cmd)
	if len(match) != 2 || strings.HasPrefix(match[1], "gate") {
		return false
	}
	words := match[1]
	if words != "hook "+event+" --agent codex" {
		return false
	}
	if windowsSlot {
		if cmd == command("windows", words, false) {
			return true
		}
		for _, probe := range []string{"Get-Command duolog", "Get-Command duolog -ErrorAction SilentlyContinue"} {
			body := "if (" + probe + ") { duolog " + words + "; exit $LASTEXITCODE }"
			if cmd == body || cmd == `powershell -NoProfile -NonInteractive -Command "`+body+`"` {
				return true
			}
		}
		return false
	}
	if cmd == command("linux", words, false) {
		return true
	}
	for _, prefix := range []string{"if command -v duolog; then exec duolog ", "if command -v duolog >/dev/null 2>&1; then exec duolog "} {
		if cmd == prefix+words+"; fi" {
			return true
		}
	}
	return false
}
