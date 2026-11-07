// Package peertest holds the fakes of the peer tests, the PaneAgent of an
// agent and the Pane backend, and the contracts that every PaneAgent and
// every Pane must meet. Only tests import it.
package peertest

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/screen"
)

// PaneAgent is the port under test. It repeats the method set of
// peer.PaneAgent, which a test of package peer cannot import from here
// without a cycle.
type PaneAgent interface {
	Argv(r route.Route, m mode.Mode, p corelaunch.Process) []string
	Settings() map[string]any
	Offer(s screen.Screen) (screen.Offer, bool)
}

// PaneSupport is peer.PaneSupport under the same cycle rule.
type PaneSupport struct {
	Agent PaneAgent
	Facts agent.PaneFacts
}

// ArgvCall is one call of Argv.
type ArgvCall struct {
	Route   route.Route
	Mode    mode.Mode
	Process corelaunch.Process
}

// Fake is a PaneAgent that records its calls and returns scripted results.
// Its Argv is the process's launch argv with the model and effort flags; its
// Offer gives Scripted when set.
type Fake struct {
	// Scripted is the offer Offer returns, when Has is true.
	Scripted screen.Offer
	Has      bool
	// Extra is what Settings returns.
	Extra map[string]any

	ArgvCalls  []ArgvCall
	OfferCalls []screen.Screen
}

func (f *Fake) Argv(r route.Route, m mode.Mode, p corelaunch.Process) []string {
	f.ArgvCalls = append(f.ArgvCalls, ArgvCall{r, m, p})
	args := p.LaunchArgv("--model", r.Model)
	if r.Effort != "" {
		args = append(args, "--effort", r.Effort)
	}
	return args
}

func (f *Fake) Settings() map[string]any { return f.Extra }

func (f *Fake) Offer(s screen.Screen) (screen.Offer, bool) {
	f.OfferCalls = append(f.OfferCalls, s)
	return f.Scripted, f.Has
}

// PaneAgentContract runs the cases every PaneAgent meets: the argv holds the
// route's model and effort and starts with the admitted launch words, a call
// changes neither the process nor the route, and a screen that is no question
// gives no offer.
func PaneAgentContract(t *testing.T, newPane func(t *testing.T) PaneAgent) {
	t.Helper()
	t.Run("argv holds model and effort", func(t *testing.T) {
		p := corelaunch.Process{Prefix: []string{"agent-bin"}, Launch: []string{"--launch-word"}}
		r := route.Route{Target: config.Target{Model: "sol-model", Effort: "high"}}
		args := newPane(t).Argv(r, mode.Ask, p)
		if len(args) < 2 || args[0] != "agent-bin" || args[1] != "--launch-word" {
			t.Fatalf("argv %v does not start with the admitted prefix and launch words", args)
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"sol-model", "high"} {
			if !strings.Contains(joined, want) {
				t.Errorf("argv %v lacks %q", args, want)
			}
		}
	})
	t.Run("argv without effort", func(t *testing.T) {
		p := corelaunch.Process{Prefix: []string{"agent-bin"}}
		args := newPane(t).Argv(route.Route{Target: config.Target{Model: "sol-model"}}, mode.Ask, p)
		if !slices.Contains(strings.Fields(strings.Join(args, " ")), "sol-model") {
			t.Fatalf("argv %v lacks the model", args)
		}
	})
	t.Run("argv leaves its input alone", func(t *testing.T) {
		p := corelaunch.Process{Prefix: []string{"agent-bin"}, Launch: []string{"--launch-word"}}
		pane := newPane(t)
		r := route.Route{Target: config.Target{Model: "m", Effort: "low"}}
		first := pane.Argv(r, mode.Plan, p)
		second := pane.Argv(r, mode.Plan, p)
		if !slices.Equal(first, second) {
			t.Fatalf("argv differs between calls: %v then %v", first, second)
		}
		if !slices.Equal(p.Prefix, []string{"agent-bin"}) || !slices.Equal(p.Launch, []string{"--launch-word"}) {
			t.Fatalf("argv changed the process: %+v", p)
		}
	})
	t.Run("no offer on a plain screen", func(t *testing.T) {
		if offer, ok := newPane(t).Offer(screen.Screen{State: screen.Ready, Text: "idle"}); ok {
			t.Fatalf("offer %+v on a ready screen", offer)
		}
	})
}

// PaneOfferContract checks one screen text of a plugin question against a
// pane: want is true for an agent that offers plugins, and then the offer
// must name the plugin of the text.
func PaneOfferContract(t *testing.T, newPane func(t *testing.T) PaneAgent, question screen.Screen, want bool, plugin string) {
	t.Helper()
	offer, ok := newPane(t).Offer(question)
	if ok != want {
		t.Fatalf("Offer ok = %v, want %v (%+v)", ok, want, offer)
	}
	if want && offer.Plugin != plugin {
		t.Fatalf("offer plugin %q, want %q", offer.Plugin, plugin)
	}
}
