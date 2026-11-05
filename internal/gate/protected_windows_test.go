package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestProtectedWindowsAliases(t *testing.T) {
	h := newProtectedHome(t)
	p := broadPolicy(t, h.home)
	t.Setenv("HOME", h.home)
	t.Setenv("USERPROFILE", h.home)
	salt := filepath.Join(h.telemetry, "salt")
	volume := filepath.VolumeName(salt)
	if len(volume) != 2 {
		t.Skipf("temporary folder %s is not on a drive", salt)
	}
	share := volume[:1] + "$" + salt[2:]
	read := func(target string) Action {
		return Action{Kind: "read", Target: target, Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()}
	}
	command := func(line string) Action {
		return Action{Kind: "exec", Target: line, Shell: "powershell", Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()}
	}
	cases := map[string]Action{
		"upper case":           read(strings.ToUpper(salt)),
		"lower case balance":   read(strings.ToLower(filepath.Join(h.balance, "state.json"))),
		"forward slashes":      read(filepath.ToSlash(salt)),
		"upper case command":   command("Get-Content " + strings.ToUpper(salt)),
		"extended":             command(`Get-Content \\?\` + salt),
		"extended read":        read(`\\?\` + salt),
		"extended unc":         command(`Get-Content \\?\UNC\localhost\` + share),
		"unc":                  command(`Get-Content \\localhost\` + share),
		"unc read":             read(`\\127.0.0.1\` + volume[:1] + "$" + filepath.Join(h.balance, "x")[2:]),
		"device":               command(`Get-Content \\.\` + salt),
		"stream":               read(salt + "::$DATA"),
		"named stream":         read(salt + ":hidden"),
		"stream command":       command("Get-Content " + salt + "::$DATA"),
		"trailing dot":         read(salt + "."),
		"trailing dot command": command("Get-Content " + salt + ". "),
		"ancestor search":      command("Select-String -Path x " + h.work + "; Get-ChildItem -Recurse " + h.home),
	}

	// A directory junction, made the way the telemetry tests plant one.
	junction := filepath.Join(t.TempDir(), "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, h.base).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	cases["junction"] = read(filepath.Join(junction, "telemetry", "salt"))
	cases["junction missing"] = read(filepath.Join(junction, "balance", "new.json"))
	cases["junction command"] = command("Get-Content " + filepath.Join(junction, "telemetry", "v1", "_account", "2026-11-05.ndjson"))
	cases["junction search"] = command("rg -uu token " + junction)

	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			for _, m := range allModes {
				if d := Evaluate(p, LeadRules{}, m, a); d.Kind != Deny {
					t.Errorf("%q in %s: %+v, want deny", a.Target, m, d)
				}
			}
		})
	}
	t.Run("8.3", func(t *testing.T) {
		short := testbin.ShortPath(t, h.telemetry)
		if _, err := os.Stat(short); err != nil {
			t.Fatal(err)
		}
		assertProtected(t, p, read(filepath.Join(short, "salt")))
		assertProtected(t, p, command("Get-Content "+filepath.Join(short, "v1", "_account", "2026-11-05.ndjson")))
	})
	t.Run("sibling", func(t *testing.T) {
		assertNormal(t, p, read(strings.ToUpper(filepath.Join(h.base, "telemetry-old", "notes.txt"))))
	})
}
