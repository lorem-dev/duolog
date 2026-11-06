package gate

import (
	"path/filepath"
	"slices"
	"strings"
)

// anyProgram runs code or fetches it from its arguments, so "python *" would
// allow every script and "make *" any makefile (make -f). A version suffix
// counts too: python3.12 is python.
var anyProgram = []string{"make", "python", "node", "nodejs", "deno", "bun", "ruby", "perl", "php", "lua", "julia", "rscript", "java", "dotnet", "awk", "gawk", "nawk", "mawk",
	"osascript", "curl", "wget", "npx", "pnpx", "bunx", "uvx", "pipx", "ssh", "scp", "rsync", "docker", "podman", "kubectl"}

// runSubcommands start a program named in the arguments: "uv run *" or
// "go run *" would allow any script.
var runSubcommands = []string{"run", "exec", "dlx", "x"}

// RememberPattern lets the arguments vary only after a program and its plain
// subcommand words, up to three words: ".venv/bin/ruff check app/a.py" becomes
// ".venv/bin/ruff check *" and "npm run lint -- --fix" "npm run lint *". A
// word with an option dash, a path or a value ends the plain words. A program
// with no subcommand, one that runs any code, or a run subcommand is
// remembered exactly as it was approved.
func RememberPattern(command string) string {
	words := strings.Fields(command)
	base := 1
	for base < len(words) && base < 3 && !strings.HasPrefix(words[base], "-") && !strings.ContainsAny(words[base], "/.=:@") {
		base++
	}
	program := strings.TrimRight(strings.ToLower(strings.TrimSuffix(filepath.Base(words[0]), ".exe")), "0123456789.")
	if base == 1 || base == len(words) || slices.Contains(anyProgram, program) || slices.Contains(runSubcommands, words[base-1]) {
		return command
	}
	return strings.Join(words[:base], " ") + " *"
}
