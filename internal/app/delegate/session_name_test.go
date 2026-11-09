package delegate

import (
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/lead/leadtest"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func nameHarness(t *testing.T, name string) (*harness, *leadtest.Fake) {
	t.Helper()
	h := newHarness(t)
	fake := &leadtest.Fake{DisplayName: name}
	h.deps.Lead["claude"] = transcriptLead{Fake: fake, agent: "claude"}
	h.register(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "claude", Model: "opus"}, Mode: mode.Ask, PID: os.Getpid()})
	t.Setenv("DUOLOG_LEAD", "lead-123456")
	return h, fake
}

// A worker starts under a name that says what it is and whose it is, read from
// the lead's agent for the lead's process.
func TestWorkerSessionNameCarriesTheLeadsName(t *testing.T) {
	h, fake := nameHarness(t, "Fix the parser (v2)")
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	if got, want := h.calls[0].SessionName, "duolog-worker-implement (lead: Fix the parser (v2))"; got != want {
		t.Fatalf("SessionName = %q, want %q", got, want)
	}
	if len(fake.NamePIDs) == 0 || fake.NamePIDs[0] != os.Getpid() {
		t.Fatalf("the lead's name was read for %v, want its process %d", fake.NamePIDs, os.Getpid())
	}
}

// A lead whose name cannot be read still gets its workers: the name ends with
// the first 6 characters of the lead id.
func TestWorkerSessionNameFallsBackToTheLeadID(t *testing.T) {
	h, _ := nameHarness(t, "")
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	if got, want := h.calls[0].SessionName, "duolog-worker-implement (lead: lead-1)"; got != want {
		t.Fatalf("SessionName = %q, want %q", got, want)
	}
}
