package peer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

const lspOffer = ` LSP plugin recommendation
   LSP provides code intelligence like go-to-definition and error checking
   Plugin: gopls-lsp
   Marketplace: claude-plugins-official
   Triggered by: .go files
   Description: Go language server for code intelligence and refactoring
   Would you like to install this LSP plugin?
   ❯ 1. Yes, install
     2. No, not now
     3. Never for this plugin
     4. Disable all LSP recommendations
`

// lspOfferOnDecline is the offer with the cursor moved to "No, not now".
func lspOfferOnDecline() string {
	text := strings.Replace(lspOffer, "❯ 1. Yes, install", "  1. Yes, install", 1)
	return strings.Replace(text, "  2. No, not now", "❯ 2. No, not now", 1)
}

const installCommand = "claude plugin install gopls-lsp@claude-plugins-official"

func TestReadPluginOffer(t *testing.T) {
	offer, ok := readPluginOffer(Parse("claude", []byte(lspOffer)))
	if !ok || offer.plugin != "gopls-lsp" || offer.marketplace != "claude-plugins-official" || offer.trigger != ".go files" {
		t.Fatalf("%+v %v", offer, ok)
	}
	if _, ok := readPluginOffer(Parse("codex", []byte(fixtureText("codex-hooks-review.txt")))); ok {
		t.Fatal("hooks review read as a plugin offer")
	}
	// A plugin name that is not a plain word is not trusted as one.
	odd := strings.Replace(lspOffer, "Plugin: gopls-lsp", "Plugin: gopls; rm -rf", 1)
	if _, ok := readPluginOffer(Parse("claude", []byte(odd))); ok {
		t.Fatal("odd plugin name accepted")
	}
	// Earlier output with a numbered list does not hide the offer below it.
	above := "Steps:\n  1. Read the file\n  2. Change it\nShould I install a plugin?\n" + lspOffer
	if offer, ok := readPluginOffer(Parse("claude", []byte(above))); !ok || offer.plugin != "gopls-lsp" {
		t.Fatalf("%+v %v", offer, ok)
	}
}

func TestPluginOfferIsDeclinedAndLeadToldOnce(t *testing.T) {
	r := newRig(t, withScreens(lspOffer, lspOfferOnDecline(), claudeIdle(""), claudeIdle("")))
	e := r.open("plan")
	if e.State != Ready {
		t.Fatalf("%+v", e)
	}
	if got := strings.Join(r.keys(), ","); got != "down,enter" {
		t.Fatalf("keys %s", got)
	}
	if len(r.named("send")) != 0 {
		t.Fatalf("text typed: %v", r.named("send"))
	}
	if len(r.messages("ask")) != 0 || r.entry(e.ID).Asked != "" {
		t.Fatal("plugin offer reached the lead as a question")
	}
	notes := r.messages("note")
	if len(notes) != 1 {
		t.Fatalf("notes %+v", notes)
	}
	if n := notes[0]; n.Ref != e.ID || !strings.Contains(n.S, "gopls-lsp") || !strings.Contains(n.S, "claude-plugins-official") || !strings.Contains(n.S, ".go files") || !strings.Contains(n.S, installCommand) {
		t.Fatalf("%+v", n)
	}
	data, err := os.ReadFile(filepath.Join(r.root, ".duolog", "decisions.ndjson"))
	if err != nil || !strings.Contains(string(data), "No, not now") || strings.Contains(string(data), "Yes, install\"") {
		t.Fatalf("%s %v", data, err)
	}

	// The same offer again in the same peer is declined with no second note.
	r.rex(withScreens(lspOffer, lspOfferOnDecline(), claudeIdle("")))
	st, err := Status(r.ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != Ready || strings.Join(r.keys(), ",") != "down,enter" {
		t.Fatalf("%+v %v", st, r.keys())
	}
	if notes := r.messages("note"); len(notes) != 1 {
		t.Fatalf("second note: %+v", notes)
	}
}

func TestPluginOfferWithoutDeclineGoesToLead(t *testing.T) {
	// Only options that install or change the person's settings: none is pressed.
	offer := strings.Replace(lspOffer, "     2. No, not now\n", "", 1)
	offer = strings.Replace(offer, "3. Never", "2. Never", 1)
	offer = strings.Replace(offer, "4. Disable", "3. Disable", 1)
	r := newRig(t, withScreens(offer))
	e := r.open("plan")
	if e.State != Question || len(r.keys()) != 0 {
		t.Fatalf("%+v %v", e, r.keys())
	}
	if len(r.messages("ask")) != 1 || len(r.messages("note")) != 0 {
		t.Fatal("offer without a decline was not left to the lead")
	}
}

func TestPluginOfferDeclinedWithoutNoteUnlessLeadFirst(t *testing.T) {
	for _, value := range []string{"refuse", "reroute"} {
		t.Run(value, func(t *testing.T) {
			r := newRig(t, withScreens(lspOffer, lspOfferOnDecline(), claudeIdle(""), claudeIdle("")))
			r.cfg.Capabilities.OnMissing.Plugin = value
			e := r.open("plan")
			if e.State != Ready || strings.Join(r.keys(), ",") != "down,enter" {
				t.Fatalf("%+v %v", e, r.keys())
			}
			if len(r.messages("note")) != 0 || len(r.messages("ask")) != 0 {
				t.Fatal("lead was told")
			}
		})
	}
}

// Offers whose metadata the gate cannot read: the Plugin: line is missing, its
// value wrapped onto the next line, or the capture starts below it.
func incompleteOffers() map[string]string {
	return map[string]string{
		"missing": strings.Replace(lspOffer, "   Plugin: gopls-lsp\n", "", 1),
		"wrapped": strings.Replace(lspOffer, "Plugin: gopls-lsp", "Plugin:\n     gopls-lsp", 1),
		"cut":     lspOffer[strings.Index(lspOffer, "   Would you like"):], //nolint:gocritic // a missing marker panics and fails the test, as it should
		"question wrapped": strings.Replace(strings.Replace(lspOffer, "   Plugin: gopls-lsp\n", "", 1),
			"install this LSP plugin?", "install this\n   LSP plugin?", 1),
	}
}

func TestIncompletePluginOfferIsDeclinedNotInstalled(t *testing.T) {
	for name, offer := range incompleteOffers() {
		t.Run(name, func(t *testing.T) {
			if _, ok := readPluginOffer(Parse("claude", []byte(offer))); ok {
				t.Fatal("metadata read from an incomplete offer")
			}
			if !installQuestion(Parse("claude", []byte(offer))) {
				t.Fatal("install question not recognised")
			}
			onDecline := strings.Replace(strings.Replace(offer, "❯ 1. Yes, install", "  1. Yes, install", 1), "  2. No, not now", "❯ 2. No, not now", 1)
			r := newRig(t, withScreens(offer, onDecline, claudeIdle(""), claudeIdle("")))
			r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "install this", Answer: "Yes, install"}}
			e := r.open("plan")
			if e.State != Ready || strings.Join(r.keys(), ",") != "down,enter" {
				t.Fatalf("%+v %v", e, r.keys())
			}
			if len(r.messages("ask")) != 0 || len(r.messages("note")) != 0 {
				t.Fatal("declined offer without a plugin name reached the lead")
			}
			data, err := os.ReadFile(filepath.Join(r.root, ".duolog", "decisions.ndjson"))
			if err != nil || !strings.Contains(string(data), "No, not now") || strings.Contains(string(data), "Yes, install\"") {
				t.Fatalf("%s %v", data, err)
			}
		})
	}
}

func TestIncompletePluginOfferWithoutApprovedDeclineGoesToLead(t *testing.T) {
	// Without metadata only "No, not now" declines; a bare "No" or "Not now" is
	// not trusted, and a text rule still cannot install.
	for name, offer := range incompleteOffers() {
		t.Run(name, func(t *testing.T) {
			offer = strings.Replace(offer, "2. No, not now", "2. Not now", 1)
			r := newRig(t, withScreens(offer))
			r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "install this", Answer: "Yes, install"}}
			e := r.open("plan")
			if e.State != Question || len(r.keys()) != 0 {
				t.Fatalf("%+v %v", e, r.keys())
			}
			if len(r.messages("ask")) != 1 || len(r.messages("note")) != 0 {
				t.Fatal("offer was not left to the lead")
			}
		})
	}
}

func TestInstallQuestionNeverReachesTextRules(t *testing.T) {
	// A Codex screen has no plugin metadata either; a text rule cannot install.
	r := newRig(t, withScreens(incompleteOffers()["missing"]))
	r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "install this", Answer: "Yes, install"}}
	e := r.open("implement")
	if r.entry(e.ID).Agent != "codex" || e.State != Question || len(r.keys()) != 0 {
		t.Fatalf("%+v %v", e, r.keys())
	}
	// Other questions still meet text rules.
	if installQuestion(Parse("claude", []byte(strings.Replace(lspOffer, "install this LSP plugin?", "continue?", 1)))) {
		t.Fatal("a question without install read as an install question")
	}
}

func TestPluginOfferIgnoresAutoAnswerRules(t *testing.T) {
	// A text rule cannot turn the offer into an install.
	r := newRig(t, withScreens(lspOffer, lspOfferOnDecline(), claudeIdle(""), claudeIdle("")))
	r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "install this LSP plugin", Answer: "Yes, install"}}
	e := r.open("plan")
	if e.State != Ready || strings.Join(r.keys(), ",") != "down,enter" {
		t.Fatalf("%+v %v", e, r.keys())
	}
}
