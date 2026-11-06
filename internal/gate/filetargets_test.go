package gate

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// onVolume gives the absolute paths of a review command the native volume on
// Windows, where /etc alone is not absolute. Elsewhere it returns the command
// as the review wrote it.
func onVolume(root, command string) string {
	volume := absoluteTestPath(root, "")
	if volume == "" {
		return command
	}
	for _, folder := range []string{"/etc", "/tmp", "/outside", "/srv"} {
		command = strings.ReplaceAll(command, folder, volume+folder)
	}
	return command
}

// wantPerson checks that a command reaches the person, in each mode that asks:
// a plain allow or a lead-only ask would let it read or write outside.
func wantPerson(t *testing.T, p config.Policy, root string, commands ...string) {
	t.Helper()
	for _, command := range commands {
		command = onVolume(root, command)
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		if !ExecOutside(a) {
			t.Errorf("%q: ExecOutside false", command)
		}
		for _, m := range []mode.Mode{mode.Ask, mode.Edits} {
			if d := Evaluate(p, LeadRules{}, m, a); !PersonOnly(d) {
				t.Errorf("%s: %q: %+v, want the person's ask", m, command, d)
			}
		}
	}
}

// wantDeny checks that a command is denied in every mode, with the rule given.
func wantDeny(t *testing.T, p config.Policy, root, rule string, commands ...string) {
	t.Helper()
	for _, command := range commands {
		for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
			a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
			if d := Evaluate(p, LeadRules{}, m, a); d.Kind != Deny || !strings.Contains(d.Rule, rule) {
				t.Errorf("%s: %q: %+v, want a deny by %q", m, command, d, rule)
			}
		}
	}
}

// wantLead checks that a command inside the work root stays the lead's ask:
// the fixes judge the files a command names, not every command of its kind.
func wantLead(t *testing.T, p config.Policy, root string, commands ...string) {
	t.Helper()
	for _, command := range commands {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		if ExecOutside(a) {
			t.Errorf("%q: ExecOutside true", command)
		}
		if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind == Deny || PersonOnly(d) {
			t.Errorf("%q: %+v, want no deny and no person's ask", command, d)
		}
	}
}

// A sed script reads the file of r and R and writes the file of w, W and the
// s///w flag, with or without a blank before the name. The e command and the
// s///e flag run a command. The gate judged the script as one relative path.
func TestSedScriptFilesAreJudged(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	wantPerson(t, p, root,
		// The review's commands.
		`sed '1r/etc/passwd' go.mod`,
		`sed '1R/etc/passwd' go.mod`,
		`sed 's/x/y/w/tmp/out' go.mod`,
		`sed -n '/x/w/tmp/out' go.mod`,
		// The task's command, with a blank, and the other ways to give a script.
		`sed -n 'w /tmp/out' x`,
		`sed -n -e 1p -e 'W/tmp/out' x`,
		`sed -n --expression='1r ../peer/go.mod' x`,
		`sed -ne '$r../peer/go.mod' x`,
		`sed -n '/a/,/b/{w../out
}' x`,
		`sed 's/x/y/gw ../out' go.mod`,
		`sed 's|x|y|w../out' go.mod`,
		`sed '1!G;h;$!d;r ../peer/x' go.mod`,
		`sed -f ../peer/script.sed go.mod`,
		`sed -i.bak s/a/b/ ../peer/go.mod`,
		`sudo sed '1r../x' go.mod`,
		`if sed '1r../x' go.mod; then :; fi`,
		`sh -c "sed '1r../x' go.mod"`,
		// e runs a command, so the gate cannot name what it reads.
		`sed '1e cat go.mod' go.mod`,
		`sed 's/x/y/e' go.mod`,
		// A script the gate cannot read is judged as reaching outside.
		`sed '1k' go.mod`,
	)
	// The file of r, R, w and W, and of -f, is judged by the read and write
	// rules, so state and secrets stay denied.
	wantDeny(t, p, root, RuleState,
		`sed -n '1r.duolog/bus.ndjson' go.mod`,
		`sed -n '$R.duolog/sessions.json' go.mod`,
	)
	// A file in a word of its own was denied before, by the write rule for
	// .duolog, and still is.
	wantDeny(t, p, root, "",
		`sed -n '1R .duolog/bus.ndjson' go.mod`,
		`sed -f .duolog/bus.ndjson go.mod`,
		`sed s/a/b/ .duolog/bus.ndjson`,
	)
	wantDeny(t, p, root, "read",
		`sed '1r.env' go.mod`,
		`sed -e '$R secrets/x' go.mod`,
	)
	wantDeny(t, p, root, "write",
		`sed 'w.git/config' go.mod`,
		`sed -n 'W .git/hooks/pre-commit' go.mod`,
		`sed 's/a/b/w.duolog/x' go.mod`,
		`sed -i s/a/b/ .git/config`,
		`sed s/a/b/ -i .git/config`,
	)
	// A script that names no file, or files inside the work root, stays the
	// lead's ask. (A script word that starts with / was judged as an outside
	// path before, and still is.)
	wantLead(t, p, root,
		`sed s/a/b/ go.mod`,
		`sed -i s/a/b/g go.mod`,
		`sed -i.bak -e 's/a/b/' -e '$d' go.mod`,
		`sed -E 's|(a)|\1|g' go.mod`,
		`sed '$!N;s/x/y/;P;D' go.mod`,
		`sed '1~3d;0,\,re,s,,x,2g' go.mod`,
		`sed -n '2{p;q}' go.mod`,
		`sed '1r notes.txt' go.mod`,
		`sed 's/a/b/w out.txt' go.mod`,
		`sed '2i\  text' go.mod`,
		`sed ':a;N;$!ba;s/x/y/g' go.mod`,
	)
}

func TestSedTargets(t *testing.T) {
	for _, tc := range []struct {
		command       string
		reads, writes []string
		unknown       bool
	}{
		{command: `sed -n '1rw/etc/passwd' x`, reads: []string{"w/etc/passwd", "x"}},
		{command: `sed '1r/etc/passwd' go.mod`, reads: []string{"/etc/passwd", "go.mod"}},
		{command: `sed 's/x/y/w/tmp/out' go.mod`, reads: []string{"go.mod"}, writes: []string{"/tmp/out"}},
		{command: `sed -n '/x/w/tmp/out' go.mod`, reads: []string{"go.mod"}, writes: []string{"/tmp/out"}},
		{command: `sed -n 'w /tmp/out' x`, reads: []string{"x"}, writes: []string{"/tmp/out"}},
		{command: `sed -i s/a/b/ x`, reads: []string{"x"}, writes: []string{"x"}},
		{command: `sed -f s.sed x`, reads: []string{"s.sed", "x"}},
		{command: `sed --file=s.sed x`, reads: []string{"s.sed", "x"}},
		{command: `sed 'r a;w b' x`, reads: []string{"a;w b", "x"}},
		{command: `sed 's/a/b/e' x`, reads: []string{"x"}, unknown: true},
		// Scripts that name no file.
		{command: `sed -e '/^$/d' -e '/x/I d' -e '/a\/b/,+2s/a\/b/x/' x`, reads: []string{"x"}},
		{command: `sed ':a;N;$!ba;s/\n/ /g' x`, reads: []string{"x"}},
		{command: `sed -n '/start/,/end/{/x/!p}' x`, reads: []string{"x"}},
		{command: `sed 'y/abc/xyz/;2q;l 5;=' x`, reads: []string{"x"}},
		{command: "sed '1a\\\nline r /etc/x\n$d' x", reads: []string{"x"}},
		{command: `sed '#r /etc/x` + "\n" + `p' x`, reads: []string{"x"}},
		{command: `sed --unknown-option x`, unknown: true},
	} {
		got := partFileTargets(tc.command)
		if !slices.Equal(got.reads, tc.reads) || !slices.Equal(got.writes, tc.writes) || got.unknown != tc.unknown {
			t.Errorf("%q: reads %q, writes %q, unknown %v; want %q, %q, %v", tc.command, got.reads, got.writes, got.unknown, tc.reads, tc.writes, tc.unknown)
		}
	}
}
