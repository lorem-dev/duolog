package session

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/trusthome"
)

// launchers run a command for an agent without being the agent: shells, and
// programs that wrap one command and stay its parent.
var launchers = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true,
	"fish": true, "csh": true, "tcsh": true, "cmd": true, "powershell": true,
	"pwsh": true, "env": true, "nohup": true, "timeout": true, "sandbox-exec": true,
	"codex-linux-sandbox": true,
}

func launcher(name string) bool {
	name = strings.ToLower(filepath.Base(name))
	name = strings.TrimSuffix(strings.TrimPrefix(name, "-"), ".exe")
	return launchers[name]
}

// AgentPID finds the agent process that runs this command's shell, as
// LeadPID does for hooks: the nearest ancestor that is not a shell or a
// launcher. It returns 0 when the system does not name processes or the
// shell has no agent above it.
func AgentPID() int { return agentPID(os.Getppid(), parentProcess) }

func agentPID(pid int, parent func(int) (int, string)) int {
	for steps := 0; pid > 1 && steps < 32; steps++ {
		next, name := parent(pid)
		if name == "" {
			return 0
		}
		if !launcher(name) {
			return pid
		}
		if next <= 1 || next == pid {
			return 0
		}
		pid = next
	}
	return 0
}

// ValidSessionID accepts the letters, digits, dashes and underscores of
// Claude session IDs and Codex thread IDs, so an ID cannot name a path.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// FindTranscript locates a session's native transcript from its ID, for a
// lead registered without a hook payload: Claude keeps
// projects/<folder>/<id>.jsonl, Codex keeps sessions/.../rollout-*-<id>.jsonl.
// It returns "" when there is none.
func FindTranscript(agent, id, home string) string {
	if !ValidSessionID(id) {
		return ""
	}
	switch agent {
	case "claude":
		matches, _ := filepath.Glob(filepath.Join(trusthome.Claude(home), "projects", "*", id+".jsonl"))
		return newest(matches)
	case "codex":
		var matches []string
		_ = filepath.WalkDir(filepath.Join(trusthome.Codex(home), "sessions"), func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), "-"+id+".jsonl") {
				matches = append(matches, path)
			}
			return nil
		})
		return newest(matches)
	}
	return ""
}

func newest(paths []string) string {
	var best string
	var bestInfo os.FileInfo
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if bestInfo == nil || info.ModTime().After(bestInfo.ModTime()) {
			best, bestInfo = path, info
		}
	}
	return best
}
