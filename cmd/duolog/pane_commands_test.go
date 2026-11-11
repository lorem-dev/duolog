package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/plugin"
)

// paneArgv matches an argv list that starts with 'duolog' in the pane's
// source; the list holds no brackets.
var paneArgv = regexp.MustCompile(`\[\s*'duolog'((?:\s*,\s*[^,\[\]]+)*)\s*,?\s*\]`)

// paneCommands are the command lines the Claude pane runs, each with its
// expressions shown as <>.
func paneCommands(src string) [][]string {
	var out [][]string
	for _, m := range paneArgv.FindAllStringSubmatch(src, -1) {
		argv := []string{"duolog"}
		for _, part := range strings.Split(m[1], ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.HasPrefix(part, "'") && strings.HasSuffix(part, "'") {
				argv = append(argv, strings.Trim(part, "'"))
			} else {
				argv = append(argv, "<>")
			}
		}
		out = append(out, argv)
	}
	return out
}

// paneCommandProblem is why argv does not parse as a duolog command with
// known flags and its arguments, or "". An expression stands for -1 where
// the flag takes a number, and for an id elsewhere.
func paneCommandProblem(t *testing.T, argv []string) string {
	t.Helper()
	root := newRootCommand(testDeps(t))
	args := slices.Clone(argv[1:])
	for i, a := range args {
		if a == "<>" {
			args[i] = "id-1"
			if i > 0 && args[i-1] == "--from" {
				args[i] = "-1"
			}
		}
	}
	cmd, rest, err := root.Find(args)
	if err != nil {
		return err.Error()
	}
	if err := cmd.ParseFlags(rest); err != nil {
		return cmd.CommandPath() + ": " + err.Error()
	}
	if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
		return cmd.CommandPath() + ": " + err.Error()
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		return cmd.CommandPath() + ": " + err.Error()
	}
	return ""
}

// Each command line the Claude pane runs is a duolog command with flags it
// knows, so that a renamed command or flag fails here and not in the pane.
func TestPaneCommandLinesParse(t *testing.T) {
	src, err := plugin.FS.ReadFile(plugin.ModFile)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, argv := range paneCommands(string(src)) {
		got = append(got, strings.Join(argv, " "))
		if problem := paneCommandProblem(t, argv); problem != "" {
			t.Errorf("%q: %s", argv, problem)
		}
	}
	slices.Sort(got)
	got = slices.Compact(got)
	want := []string{
		"duolog peer view <> --lead <>",
		"duolog runs --snapshot --fast --lead <>",
		"duolog runs tail <> --from <> --cursor <> --json",
		"duolog watch --tab <>",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the pane runs %q, want %q", got, want)
	}
}

// The check fails a command or flag that duolog does not have.
func TestPaneCommandCheckFindsAWrongFlag(t *testing.T) {
	src := "run(['duolog', 'runs', '--snapshott', lead]); run([\n  'duolog',\n  'runs',\n  'tail',\n  t.run,\n  '--from',\n  String(t.offset),\n])"
	argvs := paneCommands(src)
	if len(argvs) != 2 {
		t.Fatalf("argvs = %q", argvs)
	}
	if paneCommandProblem(t, argvs[0]) == "" {
		t.Fatal("an unknown flag passed")
	}
	if problem := paneCommandProblem(t, argvs[1]); problem != "" {
		t.Fatalf("a good command line failed: %s", problem)
	}
}
