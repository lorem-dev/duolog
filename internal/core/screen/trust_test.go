package screen

import (
	"strings"
	"testing"
)

// A command prompt in the scrollback, with its own menu, is not the command of
// the menu below it.
func TestPromptCommandIgnoresAnOlderPromptAboveAnotherMenu(t *testing.T) {
	ask := "Would you like to run the following command?"
	old := ask + "\n\n  $ go test ./...\n\n› 1. Yes, proceed (y)\n  2. No (esc)\n"
	if got := PromptCommand(old, ask); got != "go test ./..." {
		t.Fatalf("%q", got)
	}
	stale := old + "\n• Ran go test\n\nTrust this folder?\n\n› 1. Yes, proceed\n  2. Quit\n"
	if got := PromptCommand(stale, ask); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestClaudeFolderTrustReadsTheStartupQuestion(t *testing.T) {
	s := parseAs("claude", []byte(fixture(t, "claude-trust-folder.txt")))
	label, ok := ClaudeFolderTrust(s)
	if !ok || label != "Yes, I trust this folder" {
		t.Fatalf("%q %v", label, ok)
	}
	if _, ok := CodexFolderTrust(s); ok {
		t.Fatal("Codex read Claude's question as its own")
	}
}

func TestCodexFolderTrustReadsTheStartupQuestion(t *testing.T) {
	for _, name := range []string{"codex-trust-folder.txt", "codex-trust-live.txt"} {
		s := parseAs("codex", []byte(fixture(t, name)))
		label, ok := CodexFolderTrust(s)
		if !ok || label != "Trust and continue" {
			t.Fatalf("%s: %q %v", name, label, ok)
		}
		if _, ok := ClaudeFolderTrust(s); ok {
			t.Fatalf("%s: Claude read Codex's question as its own", name)
		}
	}
}

func TestFolderTrustIgnoresOtherQuestions(t *testing.T) {
	for _, c := range []struct{ agent, file string }{
		{"claude", "claude-ready.txt"},
		{"claude", "claude-approval-prompt.txt"},
		{"claude", "claude-model-switch-question.txt"},
		{"codex", "codex-ready.txt"},
		{"codex", "codex-hooks-review.txt"},
		{"codex", "codex-approval-prompt.txt"},
	} {
		s := parseAs(c.agent, []byte(fixture(t, c.file)))
		if label, ok := ClaudeFolderTrust(s); ok {
			t.Fatalf("%s: Claude trust %q", c.file, label)
		}
		if label, ok := CodexFolderTrust(s); ok {
			t.Fatalf("%s: Codex trust %q", c.file, label)
		}
	}
}

func TestFolderTrustRefusesAThirdOption(t *testing.T) {
	s := parseAs("claude", []byte(fixture(t, "claude-trust-folder.txt")))
	s.Options = append(s.Options, Option{Number: 3, Label: "Yes, and don't ask again"})
	if label, ok := ClaudeFolderTrust(s); ok {
		t.Fatalf("a question with three options was read: %q", label)
	}
}

func TestFolderTrustRefusesRelabelledOptions(t *testing.T) {
	for _, labels := range [][2]string{
		{"No, exit", "Yes, I trust this folder and its settings"},
		{"No, exit", "Yes, trust this folder"},
		{"Exit", "Yes, I trust this folder"},
		{"Yes, I trust this folder", "Yes, I trust this folder"},
	} {
		s := parseAs("claude", []byte(fixture(t, "claude-trust-folder.txt")))
		s.Options[0].Label, s.Options[1].Label = labels[0], labels[1]
		if label, ok := ClaudeFolderTrust(s); ok {
			t.Fatalf("%v: read as %q", labels, label)
		}
	}
}

func TestFolderTrustGivesTheLabelWhateverTheOrder(t *testing.T) {
	s := parseAs("claude", []byte(fixture(t, "claude-trust-folder.txt")))
	s.Options[0], s.Options[1] = s.Options[1], s.Options[0]
	if label, ok := ClaudeFolderTrust(s); !ok || label != "Yes, I trust this folder" {
		t.Fatalf("%q %v", label, ok)
	}
}

func TestFolderTrustNeedsTheQuestionState(t *testing.T) {
	s := parseAs("claude", []byte(fixture(t, "claude-trust-folder.txt")))
	s.State = Ready
	if _, ok := ClaudeFolderTrust(s); ok {
		t.Fatal("a screen that is no question was read")
	}
}

func TestFolderTrustIgnoresMarkersInOtherOutput(t *testing.T) {
	// A model's own question that quotes the markers and reuses the labels.
	text := "Earlier output said: Accessing workspace: /x. Quick safety check.\n\n" + fixture(t, "claude-ready.txt")
	s := Screen{State: Question, Question: "Which folder?", Text: text,
		Options: []Option{{Number: 1, Label: "No, exit"}, {Number: 2, Label: "Yes, I trust this folder"}}}
	if _, ok := ClaudeFolderTrust(s); ok {
		t.Fatal("markers in other output were read as the startup question")
	}
	if paths := ClaudeFolderPaths(s); len(paths) != 0 {
		t.Fatalf("paths from other output: %v", paths)
	}
}

func TestFolderTrustPathsAreTheFoldersTheScreenNames(t *testing.T) {
	claude := parseAs("claude", []byte(fixture(t, "claude-trust-folder.txt")))
	if got := ClaudeFolderPaths(claude); len(got) != 2 || got[1] != "/tmp/probe/t/ci" {
		t.Fatalf("%q", got)
	}
	codex := parseAs("codex", []byte(fixture(t, "codex-trust-live.txt")))
	if got := CodexFolderPaths(codex); len(got) != 1 || got[0] != "/Users/user/lorem/duolog" {
		t.Fatalf("%q", got)
	}
	if got := ClaudeFolderPaths(codex); len(got) != 0 {
		t.Fatalf("Claude read Codex's paths: %q", got)
	}
}

func TestOffersFolderTrustSeesAnyTrustLabel(t *testing.T) {
	s := parseAs("claude", []byte(fixture(t, "claude-trust-folder.txt")))
	s.Options = append(s.Options, Option{Number: 3, Label: "Review settings first"})
	if !OffersFolderTrust(s) {
		t.Fatal("a trust label among three options was missed")
	}
	if !OffersFolderTrust(parseAs("codex", []byte(fixture(t, "codex-trust-live.txt")))) {
		t.Fatal("Codex's trust label was missed")
	}
	if OffersFolderTrust(parseAs("claude", []byte(fixture(t, "claude-approval-prompt.txt")))) {
		t.Fatal("an approval prompt offers no trust")
	}
}

// A trust screen whose labels this version does not know is still a trust
// screen, by its text.
func TestOffersFolderTrustSeesATrustScreenByItsText(t *testing.T) {
	for _, text := range []string{
		"Accessing workspace:\n /x\n",
		"Quick safety check: is this yours?",
		"Do you trust the files in this folder?",
		"  Trust this folder? Codex can read",
		"  Folder access\n  /x",
	} {
		s := Screen{State: Question, Question: "q", Text: text,
			Options: []Option{{Number: 1, Label: "Yes, proceed"}, {Number: 2, Label: "No, exit"}}}
		if !OffersFolderTrust(s) {
			t.Errorf("%q: missed", text)
		}
		s.State = Ready
		if OffersFolderTrust(s) {
			t.Errorf("%q: a screen that is no question was counted", text)
		}
	}
	other := Screen{State: Question, Text: "Do you want to proceed?", Options: []Option{{Label: "Yes"}, {Label: "No"}}}
	if OffersFolderTrust(other) {
		t.Fatal("an ordinary question was counted")
	}
}

func TestOffersFolderTrustIgnoresMarkersQuotedInOtherOutput(t *testing.T) {
	opts := []Option{{Number: 1, Label: "Yes"}, {Number: 2, Label: "No"}}
	far := "Accessing workspace: /x\n" + strings.Repeat("output\n", 40) + "Proceed?\n1. Yes\n2. No"
	mid := "He said Accessing workspace: /x and Quick safety check\nProceed?\n1. Yes\n2. No"
	for name, text := range map[string]string{"far above the options": far, "inside a line": mid} {
		if OffersFolderTrust(Screen{State: Question, Text: text, Options: opts}) {
			t.Errorf("%s: counted", name)
		}
	}
	if !OffersFolderTrust(Screen{State: Question, Text: "Quick safety check\nProceed?\n1. Yes\n2. No", Options: opts}) {
		t.Error("a marker right above the options was missed")
	}
}

func TestFolderPathsJoinAWrappedPath(t *testing.T) {
	text := "Accessing workspace:\n\n /a/very/long/path/that/wraps/\nacross/two/lines\n\nQuick safety check: ok\n"
	got := folderPaths(text, "Accessing workspace:", "Quick safety check")
	if len(got) != 1 || got[0] != "/a/very/long/path/that/wraps/across/two/lines" {
		t.Fatalf("%q", got)
	}
}

func TestFolderTrustNeedsBothOptions(t *testing.T) {
	s := parseAs("claude", []byte(fixture(t, "claude-trust-folder.txt")))
	s.Options = s.Options[1:]
	if _, ok := ClaudeFolderTrust(s); ok {
		t.Fatal("a question without its refusal was read")
	}
}
