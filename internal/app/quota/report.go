package quota

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/record"
)

// Result is what duolog quota found for one agent.
type Result struct {
	*account.Snapshot
	Unavailable   string `json:"unavailable,omitempty"`
	Cleared       bool   `json:"cleared_unavailable,omitempty"`
	RegistryError string `json:"registry_error,omitempty"`
	// Reports explain the billing of the worker, peer and lead, by shape.
	Reports map[string]paidruns.Report `json:"reports,omitempty"`
	// LeadUnknown says why the lead has no report; LeadMismatch how its
	// verdict differs from the worker's.
	LeadUnknown  string `json:"lead_unknown,omitempty"`
	LeadMismatch string `json:"lead_mismatch,omitempty"`
}

// Marks are the unavailable marks of the project's session registry.
type Marks interface {
	ClearUnavailable(agent string) error
}

// Request is one duolog quota run: the account gate, the person's config,
// so the worker's process, its proxy and CA variables included, is the one
// read, and where the processes run. Unavailable are the agents the session
// registry marks unavailable, RegistryErr why it could not be read, and Marks
// the registry that clears a mark, nil without one. Lead gives the lead's
// process from an observation of it, false without one. Observe receives each
// successful read, nil for none.
type Request struct {
	Gate        *paidruns.Gate
	Config      *config.Config
	Home, Root  string
	GOOS        string
	Environ     []string
	Unavailable map[string]bool
	RegistryErr error
	Marks       Marks
	Lead        func(agent string) (corelaunch.ProcessInput, bool)
	Observe     func(record.QuotaObservation)
}

// Collect reads and reports each agent through the gate: the worker's free
// read and billing, the peer's billing, and the lead's from an observation,
// never run. A healthy read clears the agent's unavailable mark. It returns
// the results by agent and how many agents were read.
func Collect(ctx context.Context, r Request) (map[string]Result, int) {
	gate, cfg, home, root, environ := r.Gate, r.Config, r.Home, r.Root, r.Environ
	results := make(map[string]Result)
	read := 0
	for _, agent := range []string{"claude", "codex"} {
		result := Result{Reports: map[string]paidruns.Report{}}
		// The worker's free read, through the shared build and read path.
		worker, err := gate.Report(ctx, corelaunch.ProcessInput{Agent: agent, Shape: corelaunch.Worker,
			Variant: corelaunch.Free, Config: cfg, Environ: environ,
			Locations: corelaunch.Locations{Home: home, Root: root, GOOS: r.GOOS, Environ: environ}})
		if worker.Assessment.Agent != "" {
			result.Reports["worker"] = worker
		}
		s, readOK := gate.Cache.Latest(agent)
		if err != nil || !readOK {
			if err == nil {
				err = errors.New("no quota read")
			}
			result.Unavailable = err.Error()
		} else {
			read++
			result.Snapshot = &s
			if r.observes(s) {
				r.Observe(Observation(agent, s))
			}
			if r.RegistryErr != nil {
				result.RegistryError = "cannot read session registry"
			} else if r.Unavailable[agent] && healthyNow(s) {
				if r.Marks.ClearUnavailable(agent) != nil {
					result.RegistryError = "cannot clear unavailable mark"
				} else {
					result.Cleared = true
				}
			}
		}
		// The peer's own process; it may be routed otherwise.
		peer, _ := gate.Report(ctx, corelaunch.ProcessInput{Agent: agent, Shape: corelaunch.Peer,
			Variant: corelaunch.Free, Config: cfg, Environ: environ,
			Locations: corelaunch.Locations{Home: home, Root: root, GOOS: r.GOOS, Environ: environ}})
		if peer.Assessment.Agent != "" {
			result.Reports["peer"] = peer
		}
		// The lead only from an observation of it, reported and never run.
		if in, ok := r.Lead(agent); !ok {
			result.LeadUnknown = "no observation of the lead's command and environment"
		} else if lead, _ := gate.Report(ctx, in); lead.Assessment.Agent != "" {
			result.Reports["lead"] = lead
			if worker.Assessment.Agent != "" {
				result.LeadMismatch = paidruns.CompareLead(lead, worker)
			}
		}
		results[agent] = result
	}
	return results, read
}

// observes reports whether s goes to telemetry: an earlier read used in place
// of a failed one is no new observation.
func (r Request) observes(s account.Snapshot) bool { return r.Observe != nil && !s.Recalled }

// healthyNow reports whether s shows the agent healthy now. An earlier read
// used in place of a failed one proves nothing new.
func healthyNow(s account.Snapshot) bool { return !s.Recalled && s.Healthy(time.Now()) }

// Print writes the results as duolog quota shows them, at now.
func Print(w io.Writer, results map[string]Result, now time.Time) error {
	var b strings.Builder
	for _, agent := range []string{"claude", "codex"} {
		r := results[agent]
		if r.Unavailable != "" {
			fmt.Fprintf(&b, "%-7s unavailable: %s\n", agent, r.Unavailable)
			printBilling(&b, agent, r)
			continue
		}
		fmt.Fprintf(&b, "%-7s", agent)
		if r.Plan != "" {
			fmt.Fprintf(&b, " plan %s", r.Plan)
		}
		b.WriteByte('\n')
		windows := func(ws []account.Window, prefix string) {
			for _, window := range ws {
				label := window.Kind
				if label == "session" {
					label = "5h"
				} else if label == "weekly_all" {
					label = "weekly"
				} else if label == "weekly_scoped" {
					label = "weekly"
				} else if family, ok := strings.CutPrefix(label, "weekly_"); ok && family != "" {
					label = "weekly " + family
				} else if label == "other" && window.Group != "" {
					label = window.Group
				}
				if window.Model != "" {
					if label == "other" {
						label = window.Model
					} else {
						label += " " + window.Model
					}
				}
				fmt.Fprintf(&b, "  %-7s %g%% used, %s", prefix+label, window.UsedPercent, resetText(window.ResetsAt, now))
				if window.Severity != "" && window.Severity != "normal" && window.Severity != "unknown" {
					fmt.Fprintf(&b, ", %s", window.Severity)
				}
				b.WriteByte('\n')
			}
		}
		windows(r.Windows, "")
		keys := make([]string, 0, len(r.ModelLimits))
		for family := range r.ModelLimits {
			keys = append(keys, family)
		}
		sort.Strings(keys)
		for _, family := range keys {
			windows(r.ModelLimits[family], family+" ")
		}
		if r.ExtraUsage != nil {
			if !r.ExtraUsage.Enabled {
				b.WriteString("  extra usage off\n")
			} else {
				fmt.Fprintf(&b, "  extra usage on, %g credits used", r.ExtraUsage.UsedCredits)
				if r.ExtraUsage.MonthlyLimit != nil {
					fmt.Fprintf(&b, " of %g", *r.ExtraUsage.MonthlyLimit)
				}
				b.WriteByte('\n')
			}
		}
		if hasCredits(r.Credits) {
			fmt.Fprintf(&b, "  credits available: %t", r.Credits.HasCredits)
			if r.Credits.Unlimited {
				b.WriteString(", unlimited")
			}
			if r.Credits.Balance != nil {
				fmt.Fprintf(&b, ", balance %s", *r.Credits.Balance)
			}
			b.WriteByte('\n')
		}
		if r.Rejected {
			b.WriteString("  quota rejected\n")
		}
		if r.SpendControlReached {
			b.WriteString("  spend control reached\n")
		}
		if r.Cleared {
			b.WriteString("  cleared unavailable mark\n")
		}
		if r.RegistryError != "" {
			fmt.Fprintf(&b, "  %s\n", r.RegistryError)
		}
		printBilling(&b, agent, r)
	}
	// What belongs to the account comes once, after every agent.
	for _, agent := range []string{"claude", "codex"} {
		if report, ok := results[agent].Reports["worker"]; ok {
			for _, line := range report.AccountLines() {
				fmt.Fprintf(&b, "%s\n", line)
			}
			break
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// printBilling adds how the agent's worker, peer and lead are billed, and
// how the lead differs from the worker. Names and numbers only.
func printBilling(b *strings.Builder, agent string, r Result) {
	for _, shape := range []string{"worker", "peer", "lead"} {
		report, ok := r.Reports[shape]
		if !ok {
			continue
		}
		for _, line := range report.Lines() {
			fmt.Fprintf(b, "  %s\n", line)
		}
	}
	if r.LeadUnknown != "" {
		fmt.Fprintf(b, "  %s lead: unknown (%s)\n", agent, r.LeadUnknown)
	}
	if r.LeadMismatch != "" {
		fmt.Fprintf(b, "  %s\n", r.LeadMismatch)
	}
}

func hasCredits(credits *account.Credits) bool {
	if credits == nil {
		return false
	}
	if credits.HasCredits || credits.Unlimited {
		return true
	}
	if credits.Balance != nil {
		balance, err := strconv.ParseFloat(*credits.Balance, 64)
		return err == nil && balance != 0
	}
	return false
}

func resetText(reset, now time.Time) string {
	if reset.IsZero() {
		return "reset unknown"
	}
	if !reset.After(now) {
		return "reset passed; read again"
	}
	minutes := int(reset.Sub(now) / time.Minute)
	if minutes < 1 {
		return "resets in <1m"
	}
	days, hours, mins := minutes/(24*60), (minutes/60)%24, minutes%60
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("resets in %dd %dh", days, hours)
		}
		return fmt.Sprintf("resets in %dd", days)
	}
	if hours > 0 {
		if mins > 0 {
			return fmt.Sprintf("resets in %dh %dm", hours, mins)
		}
		return fmt.Sprintf("resets in %dh", hours)
	}
	return fmt.Sprintf("resets in %dm", mins)
}
