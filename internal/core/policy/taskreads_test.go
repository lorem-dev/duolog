package policy

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// readsFixture is a work root and, outside it, a folder of docs that a task
// lists under Read, a secret folder beside them, duolog state, a folder with
// a symlink out and folders that each hold one thing a search must not read.
// links is false where the host cannot make symlinks.
type readsFixture struct {
	work, out string
	links     bool
}

// dirty are, per folder, the files that keep a search of the folder from
// counting as a read of what the task lists.
var dirty = map[string]string{"key": "key/server.key", "vault": "vault/secrets/prod.yaml", "env": "env/.env", "state": "state/.duolog/bus.ndjson", "hidden": "hidden/.netrc",
	"upper": "upper/a/SERVER.KEY", "short": "short/DUOLOG~1/bus.ndjson"}

func newReadsFixture(t *testing.T) readsFixture {
	t.Helper()
	f := readsFixture{work: pathutil.Resolved(t.TempDir()), out: pathutil.Resolved(t.TempDir())}
	files := []string{"docs/a.md", "docs/b.txt", "docs/sub/c.md", "secret/s.md", ".duolog/bus.ndjson", "linked/a.md"}
	for folder, file := range dirty {
		files = append(files, folder+"/notes.md", file)
	}
	for _, file := range files {
		path := f.path(file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.links = os.Symlink(f.path("secret/s.md"), f.path("linked/link.md")) == nil &&
		os.Symlink(f.path("secret"), f.path("linked/esc")) == nil
	return f
}

// path is a file of the outside folder, as the host spells it.
func (f readsFixture) path(name string) string { return filepath.Join(f.out, filepath.FromSlash(name)) }

// word is a file of the outside folder as a shell command names it.
func (f readsFixture) word(name string) string { return filepath.ToSlash(f.path(name)) }

// read is a read tool's action as the hook builds it, relative to the work root.
func (f readsFixture) read(target string, reads ...string) Action {
	return Action{Kind: "read", Target: RelativeTarget(f.work, f.work, target), Role: "review_final", Work: f.work, Reads: reads, WideRead: wideSkillRoot}
}

func (f readsFixture) exec(command string, reads ...string) Action {
	return Action{Kind: "exec", Target: command, Role: "review_final", Work: f.work, Cwd: f.work, State: f.work, Reads: reads, WideRead: wideSkillRoot}
}

// A path that the task lists under Read is readable for a read-only worker,
// by the read tools and by named read commands, in every mode.
func TestTaskReadsAllowListedPaths(t *testing.T) {
	f := newReadsFixture(t)
	p := policy(t)
	lead := LeadRules{RoleSandbox: "read-only"}
	glob, folder := f.path("docs/*.md"), f.path("docs")
	for name, a := range map[string]Action{
		"Read of a glob match":         f.read(f.path("docs/a.md"), glob),
		"Read of a file in the folder": f.read(f.path("docs/sub/c.md"), folder),
		"Grep of the folder":           f.read(folder, folder),
		"Glob pattern in the folder":   f.read(f.path("docs/*.md"), folder),
		"Glob pattern of the entry":    f.read(f.path("docs/*.md"), glob),
		"cat":                          f.exec("cat "+f.word("docs/a.md"), glob),
		"wc":                           f.exec("wc -l "+f.word("docs/a.md"), glob),
		"head and tail":                f.exec("head -5 "+f.word("docs/a.md")+" && tail -3 "+f.word("docs/sub/c.md"), folder),
		"rg in the folder":             f.exec("rg -n x "+f.word("docs"), folder),
		"ls of the folder":             f.exec("ls "+f.word("docs"), folder),
	} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Full} {
			if d := Evaluate(p, lead, m, a); d.Kind != Allow || d.Rule != RuleTaskRead && a.Kind == "read" {
				t.Errorf("%s in %s: %+v, want allow by %s", name, m, d, RuleTaskRead)
			}
		}
	}
}

// Read opens reads only: of the listed paths, through no .., never a write
// or a program, and every deny still holds.
func TestTaskReadsOpenNothingElse(t *testing.T) {
	f := newReadsFixture(t)
	p := policy(t)
	lead := LeadRules{RoleSandbox: "read-only"}
	glob, folder := f.path("docs/*.md"), f.path("docs")
	person := map[string]Action{
		"another extension":        f.read(f.path("docs/b.txt"), glob),
		"below the glob":           f.read(f.path("docs/sub/c.md"), glob),
		"the folder of a glob":     f.read(folder, glob),
		"a folder beside":          f.read(f.path("secret/s.md"), glob, folder),
		"a glob beside":            f.read(f.path("*/s.md"), folder),
		"no Read list":             f.read(f.path("docs/a.md")),
		"cat with no Read list":    f.exec("cat " + f.word("docs/a.md")),
		"cat through ..":           f.exec("cat "+f.word("docs")+"/../secret/s.md", folder),
		"cat of a file beside too": f.exec("cat "+f.word("docs/a.md")+" "+f.word("secret/s.md"), folder),
	}
	for name, a := range person {
		if d := Evaluate(p, lead, mode.Ask, a); !PersonOnly(d) {
			t.Errorf("%s: %+v, want the person's ask", name, d)
		}
	}
	denied := map[string]Action{
		"a secret in the folder": f.read(f.path("env/.env"), f.path("env")),
		"duolog state":           f.read(f.path(".duolog/bus.ndjson"), f.out),
		"a write":                {Kind: "write", Target: RelativeTarget(f.work, f.work, f.path("docs/a.md")), Role: "review_final", Work: f.work, Reads: []string{folder}},
		"a copy":                 f.exec("cp "+f.word("docs/a.md")+" a.md", folder),
		"a redirect":             f.exec("cat "+f.word("docs/a.md")+" > a.md", folder),
		"a shell":                f.exec("sh "+f.word("docs/a.md"), folder),
		"the file as a program":  f.exec(f.word("docs/a.md"), folder),
	}
	for name, a := range denied {
		if d := Evaluate(p, lead, mode.Ask, a); d.Kind != Deny {
			t.Errorf("%s: %+v, want deny", name, d)
		}
	}
	// A deny rule of the project wins over the Read list.
	strict := p
	strict.Rules = append([]config.Rule{{Action: "read", Match: "**/a.md", Decision: Deny}}, slices.Clone(p.Rules)...)
	for _, a := range []Action{f.read(f.path("docs/a.md"), folder), f.exec("cat "+f.word("docs/a.md"), folder)} {
		if d := Evaluate(strict, lead, mode.Ask, a); d.Kind != Deny {
			t.Errorf("%s under a deny rule: %+v, want deny", a.Target, d)
		}
	}
	if !f.links {
		return
	}
	// Symlinks resolve first: a listed name that leads out, and a folder with
	// a link out below it, which a recursive read could follow.
	linked := f.path("linked")
	for name, a := range map[string]Action{
		"a link out of the glob":         f.read(f.path("linked/link.md"), f.path("linked/*.md")),
		"a link out of the folder":       f.read(f.path("linked/link.md"), linked),
		"cat of a link out":              f.exec("cat "+f.word("linked/link.md"), linked),
		"a folder holding a link out":    f.read(linked, linked),
		"rg of a folder with a link out": f.exec("rg -n x "+f.word("linked"), linked),
		"a path through a link out":      f.read(f.path("linked/esc/s.md"), linked),
	} {
		if d := Evaluate(p, lead, mode.Ask, a); d.Kind == Allow {
			t.Errorf("%s: %+v, want no allow", name, d)
		}
	}
	if d := Evaluate(p, lead, mode.Ask, f.read(f.path("linked/a.md"), f.path("linked"))); d.Kind != Allow {
		t.Errorf("a file beside a link out: %+v, want allow", d)
	}
}

// A listed folder counts for a search, a listing or a glob in it only when
// nothing in it is something a search must not read: a file a read deny or
// the state rule refuses, or a hidden file, which Claude's Grep searches
// with --hidden. Its plain files stay readable one by one.
func TestTaskReadsSearchOnlyCleanFolders(t *testing.T) {
	f := newReadsFixture(t)
	p := policy(t)
	lead := LeadRules{RoleSandbox: "read-only"}
	for folder, file := range dirty {
		entry := f.path(folder)
		search := f.read(entry, entry)
		search.Search = true
		for name, a := range map[string]Action{
			"Grep":         search,
			"Glob pattern": f.read(f.path(folder+"/**/*"), entry),
			"rg":           f.exec("rg -n KEY "+f.word(folder), entry),
			"grep -rn":     f.exec("grep -rn KEY "+f.word(folder), entry),
			"ls":           f.exec("ls "+f.word(folder), entry),
			"find":         f.exec("find "+f.word(folder)+" -type f", entry),
		} {
			if d := Evaluate(p, lead, mode.Ask, a); d.Kind == Allow {
				t.Errorf("%s of a folder holding %s: %+v, want no allow", name, file, d)
			}
		}
		if d := Evaluate(p, lead, mode.Ask, f.read(f.path(folder+"/notes.md"), entry)); d.Kind != Allow {
			t.Errorf("a plain file beside %s: %+v, want allow", file, d)
		}
	}
}

// A search reaches what a symlink in its folder points at, so a folder with a
// symlink anywhere below the search root never counts, even one that stays in
// the listed folder: rg -L in a subfolder would print the .env a link there
// names. A plain file stays readable, and a link to a denied file is denied.
func TestTaskReadsSearchNoFolderWithLinks(t *testing.T) {
	f := newReadsFixture(t)
	p := policy(t)
	lead := LeadRules{RoleSandbox: "read-only"}
	entry := f.path("withlink")
	for _, file := range []string{"withlink/.env", "withlink/server.key", "withlink/sub/a.md", "plainlink/sub/a.md"} {
		if err := os.MkdirAll(filepath.Dir(f.path(file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.path(file), []byte("KEY=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if os.Symlink(f.path("withlink/.env"), f.path("withlink/sub/notes")) != nil || os.Symlink(f.path("plainlink/sub/a.md"), f.path("plainlink/sub/same.md")) != nil {
		t.Skip("this host cannot make symlinks")
	}
	sub := f.path("withlink/sub")
	grep := f.read(sub, entry)
	grep.Search = true
	plain := f.read(f.path("plainlink/sub"), f.path("plainlink"))
	plain.Search = true
	for name, a := range map[string]Action{
		"rg -L in the subfolder":       f.exec("rg -L KEY "+f.word("withlink/sub"), entry),
		"rg in the subfolder":          f.exec("rg -n KEY "+f.word("withlink/sub"), entry),
		"Grep of the subfolder":        grep,
		"Grep of a folder with a link": plain,
	} {
		if d := Evaluate(p, lead, mode.Ask, a); d.Kind == Allow {
			t.Errorf("%s: %+v, want no allow", name, d)
		}
	}
	if d := Evaluate(p, lead, mode.Ask, f.read(f.path("withlink/sub/a.md"), entry)); d.Kind != Allow {
		t.Errorf("a plain file: %+v, want allow", d)
	}
	if d := Evaluate(p, lead, mode.Ask, f.read(f.path("withlink/sub/notes"), entry)); d.Kind != Deny {
		t.Errorf("a link to .env: %+v, want deny", d)
	}
}

// A walk counts only folders and regular files as plain. Since Go 1.23 a
// Windows junction reads as irregular, not as a symlink, and rg -L follows
// it out like one; sockets, pipes and devices are no plain files either.
func TestSpecialFileTypesCountAsLinks(t *testing.T) {
	for mode, want := range map[fs.FileMode]bool{
		0: false, fs.ModeDir: false,
		fs.ModeSymlink: true, fs.ModeIrregular: true, fs.ModeSocket: true, fs.ModeNamedPipe: true,
		fs.ModeDevice: true, fs.ModeDevice | fs.ModeCharDevice: true, fs.ModeDir | fs.ModeSymlink: true,
	} {
		if got := specialType(mode); got != want {
			t.Errorf("specialType(%v) = %v, want %v", mode, got, want)
		}
	}
}

// A read command that reads the files a list names, such as wc
// --files0-from=list, is not a plain read: the gate cannot see the list. GNU
// takes any unambiguous start of a long option, so wc --files0=list and
// du --files0-fr=list count too, and wc and du ask about a long option they
// do not know, such as wc --f=list.
func TestFilesFromListIsNoPlainRead(t *testing.T) {
	p := policy(t)
	for _, command := range []string{"wc -l a.go", "wc --lines a.go", "du -sh .", "du --summarize --human-readable .", "shasum a.go", "shasum -a 256 a.go"} {
		if d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement"}); d.Kind != Allow {
			t.Errorf("%q: %+v, want allow", command, d)
		}
	}
	for _, command := range []string{"wc --files0-from=list", "wc --files0-from list", "sort --files0-from=list", "du --files0-from=list", "wc -l --files0-from=-",
		"wc --files0=list", "wc --f=list", "wc --fi list", "du --files0=list", "du --files0-fr=list", "sort --files0=list", "find -files0-from list", "sort --fil=list",
		// A checksum check reads every file its list names, from a file or stdin.
		"shasum -c list", "shasum -c", "shasum -a 256 -c list", "shasum -bc list", "shasum --check list", "shasum --ch list",
		"sha1sum -c list", "sha256sum --check list", "sha512sum -c list", "md5sum -c list", "b2sum -c list", "cksum --check list"} {
		if d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement"}); d.Kind == Allow {
			t.Errorf("%q: %+v, want no allow", command, d)
		}
		if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "exec", Target: command, Role: "review_final"}); d.Kind != Deny {
			t.Errorf("%q in a read-only role: %+v, want deny", command, d)
		}
	}
}

// The hook reads the list from DUOLOG_READ, which delegate writes; only
// absolute entries count.
func TestTaskReadsFromEnvironment(t *testing.T) {
	abs := pathutil.Resolved(t.TempDir())
	value := TaskReadsValue([]string{abs, "docs/x.md"})
	if got := TaskReads(value); !slices.Equal(got, []string{abs}) {
		t.Fatalf("TaskReads(%q) = %q", value, got)
	}
	for _, value := range []string{"", "not json", `{"a": 1}`} {
		if got := TaskReads(value); got != nil {
			t.Errorf("TaskReads(%q) = %q, want none", value, got)
		}
	}
}
