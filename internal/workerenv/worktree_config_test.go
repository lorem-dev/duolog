package workerenv_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeLocal(t *testing.T, root, window string) {
	t.Helper()
	dir := filepath.Join(root, ".duolog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.local.yaml"), []byte("workers:\n  compact_window: "+window+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A peer opened from a linked worktree gets the compact window of the main
// checkout's .duolog/config.local.yaml: duolog's project root, and with it
// the config root, is the main checkout, which linked worktrees share. The
// worktree's own local file is not read.
func TestPeerInALinkedWorktreeTakesTheMainCheckoutsLocalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	base := t.TempDir()
	main, linked := filepath.Join(base, "main"), filepath.Join(base, "main", ".worktrees", "linked")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "init", "-q")
	if err := os.WriteFile(filepath.Join(main, ".gitignore"), []byte(".worktrees/\n.duolog/config.local.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "add", ".gitignore")
	gitIn(t, main, "commit", "-q", "-m", "x")
	gitIn(t, main, "worktree", "add", "-q", linked)
	writeLocal(t, main, "500000")
	writeLocal(t, linked, "250000")

	cfg, err := configfile.Load(project.StateRoot(linked))
	if err != nil {
		t.Fatal(err)
	}
	p, err := workerenv.Build(workerenv.ProcessInput{Agent: "claude", Shape: workerenv.Peer, Variant: workerenv.Free,
		Config: cfg, Environ: []string{"HOME=" + home}, Locations: workerenv.Locations{GOOS: "linux"}})
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(p.Settings), &settings); err != nil {
		t.Fatal(err)
	}
	if got := settings.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "500000" {
		t.Fatalf("compact window %q, want the main checkout's 500000", got)
	}
}
