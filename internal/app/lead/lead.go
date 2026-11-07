// Package lead registers the running agent session as the project's lead. What
// differs per agent (which variable names the session, how to tell an
// interactive session, where the transcript is) sits behind Lead.
package lead

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/launch"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/store"
)

// Lead is what one agent knows about its own lead session.
type Lead interface {
	// Session reads the session id the agent exports to its shell.
	Session(environ []string) (id string, ok bool)
	// RefuseShell says why a shell whose environment is environ cannot be
	// the lead's, or nil when its environment alone does not rule it out.
	// Register asks it before it looks for the agent's process.
	RefuseShell(environ []string) error
	// RefuseTranscript says why session id, whose transcript is transcript
	// ("" when none was found), is not an interactive session, or nil.
	RefuseTranscript(id, transcript string) error
	// Transcript finds the native transcript of session id under home.
	Transcript(home, id string) (string, bool)
	// State reads the model, effort and mode that the transcript records.
	State(transcript string) (corelead.LeadState, error)
}

// Deps is what Register reaches outside the package. wiring fills every field.
type Deps struct {
	// Agents holds the Lead of each registered agent, keyed by agent name.
	Agents map[string]Lead
	// Facts holds the descriptor of each agent in registration order.
	Facts []agent.Descriptor
	// Registry opens the lead registry of a state folder.
	Registry func(stateDir string) (*session.Registry, error)
	// PID finds the agent process above the command's shell.
	PID func() int
	// Home gives the user's home folder.
	Home func() (string, error)
	// Now is the time an observation starts at.
	Now func() time.Time
}

// RegisterRequest is one registration: what the command line and the
// environment say.
type RegisterRequest struct {
	// Agent is the --agent flag; empty reads it from the environment.
	Agent string
	// Mode is the mode asked for; empty registers at the observed mode.
	Mode mode.Mode
	// Root is the project root.
	Root string
	// Environ is the caller's environment.
	Environ []string
}

// RegisterResult is the registered lead.
type RegisterResult struct {
	Address string
	PID     int
	Mode    mode.Mode
}

// State reads the transcript state through the Lead of agent. An agent
// without a Lead has no observation.
func State(agents map[string]Lead, agentName, transcript string) (corelead.LeadState, error) {
	l, ok := agents[agentName]
	if !ok {
		return corelead.LeadState{}, nil
	}
	return l.State(transcript)
}

// Register registers the session that runs the command as the lead, when its
// SessionStart hook did not: the session started in another folder, or was
// resumed here after a restart. The mode asked for can only be at or below the
// mode observed in the session's transcript.
func Register(ctx context.Context, d Deps, r RegisterRequest) (RegisterResult, error) {
	var res RegisterResult
	s, err := find(d, r)
	if err != nil {
		return res, err
	}
	if _, err := store.Open(r.Root); err != nil {
		return res, err
	}
	reg, err := d.Registry(filepath.Join(r.Root, ".duolog"))
	if err != nil {
		return res, err
	}
	existing, live, err := reg.Lead(s.id)
	if err != nil {
		return res, err
	}
	if live && existing.Agent != s.name {
		return res, fmt.Errorf("session %s is registered as a %s lead", s.id, existing.Agent)
	}
	if live && existing.PID > 0 && existing.PID != s.pid {
		return res, fmt.Errorf("session %s is already the live lead under process %d; use duolog mode to lower its mode", s.id, existing.PID)
	}
	if err := checkMode(r.Mode, s.observed.Mode, existing.Mode, live); err != nil {
		return res, err
	}
	// Taken after reg.Lead, which ends a record whose process is gone, so
	// that this observation resumes the session rather than predating its end.
	registered, err := reg.ObserveLead(s.observation(), d.Now())
	if err != nil {
		return res, err
	}
	if r.Mode != "" {
		if err := reg.SetMode(s.id, r.Mode); err != nil {
			return res, err
		}
	}
	_, effective, err := reg.EffectiveMode(s.id)
	if err != nil {
		return res, err
	}
	return RegisterResult{Address: session.Address(registered), PID: s.pid, Mode: effective}, nil
}

// found is the session that runs the command, as its agent shows it.
type found struct {
	name, id, transcript string
	pid                  int
	observed             corelead.LeadState
}

// find reads who the session is: its agent and id, whether its shell can be
// a lead's, the agent process above the shell, whether its transcript shows an
// interactive session and what that transcript records.
func find(d Deps, r RegisterRequest) (found, error) {
	name, id, err := identity(d, r.Agent, r.Environ)
	if err != nil {
		return found{}, err
	}
	l := d.Agents[name]
	if err := l.RefuseShell(r.Environ); err != nil {
		return found{}, err
	}
	pid := d.PID()
	if pid <= 1 {
		return found{}, fmt.Errorf("cannot find the %s process that runs this shell; run duolog lead register from the session's own shell", name)
	}
	home, err := d.Home()
	if err != nil {
		return found{}, err
	}
	transcript, _ := l.Transcript(home, id)
	if err := l.RefuseTranscript(id, transcript); err != nil {
		return found{}, err
	}
	s := found{name: name, id: id, transcript: transcript, pid: pid}
	if transcript != "" {
		s.observed, _ = l.State(transcript)
	}
	return s, nil
}

// observation holds only what the transcript showed.
func (s found) observation() corelead.LeadObservation {
	o := corelead.LeadObservation{ID: s.id, Agent: s.name, PID: &s.pid}
	if s.transcript != "" {
		o.TranscriptPath = &s.transcript
	}
	if s.observed.Model != "" {
		o.Model = &s.observed.Model
	}
	if s.observed.Effort != "" {
		o.Effort = &s.observed.Effort
	}
	if s.observed.Mode != "" {
		o.Mode = &s.observed.Mode
	}
	return o
}

// checkMode refuses a requested mode above the ceiling. The ceiling is what
// duolog observes: the transcript, then a live hook record, then the safe
// default.
func checkMode(requested, observed, recorded mode.Mode, live bool) error {
	ceiling := observed
	if ceiling == "" {
		ceiling = mode.Ask
		if live {
			ceiling = mode.Lower(recorded, recorded)
		}
	}
	if requested == "" || mode.Lower(requested, ceiling) == requested {
		return nil
	}
	if observed == "" && !live {
		return fmt.Errorf("cannot register at %s: duolog cannot observe this session's permission mode, so it registers at ask or below", requested)
	}
	return fmt.Errorf("cannot register at %s above the observed mode %s", requested, ceiling)
}

func descriptor(facts []agent.Descriptor, name string) (agent.Descriptor, bool) {
	for _, f := range facts {
		if f.Name == name {
			return f, true
		}
	}
	return agent.Descriptor{}, false
}

// identity reads the agent and session ID that the agent exports to its
// shell, from flag when it names the agent.
func identity(d Deps, flag string, environ []string) (name, id string, err error) {
	ids := make(map[string]string, len(d.Facts))
	for _, f := range d.Facts {
		ids[f.Name], _ = d.Agents[f.Name].Session(environ)
	}
	if flag != "" {
		if _, ok := descriptor(d.Facts, flag); !ok {
			return "", "", fmt.Errorf("unknown agent %q; use %s", flag, strings.Join(joined(d.Facts, func(f agent.Descriptor) string { return f.Name }), " or "))
		}
		name, id = flag, ids[flag]
	} else if name, id, err = detect(d, ids, environ); err != nil {
		return "", "", err
	}
	if id == "" {
		f, _ := descriptor(d.Facts, name)
		return "", "", fmt.Errorf("this %s session does not set %s, so duolog cannot tell which session to register; start the session in this project's folder so that its hooks register it", name, f.SessionEnv[0])
	}
	if !session.ValidSessionID(id) {
		return "", "", fmt.Errorf("session ID %q has characters a session ID never has", id)
	}
	return name, id, nil
}

// detect names the agent when no flag does: the one whose session variable is
// set, else the one whose marker the environment carries (its id is then
// empty, which the caller reports).
func detect(d Deps, ids map[string]string, environ []string) (name, id string, err error) {
	var with []agent.Descriptor
	for _, f := range d.Facts {
		if ids[f.Name] != "" {
			with = append(with, f)
		}
	}
	switch {
	case len(with) > 1:
		vars := joined(with, func(f agent.Descriptor) string { return f.SessionEnv[0] })
		flags := joined(with, func(f agent.Descriptor) string { return "--agent " + f.Name })
		return "", "", fmt.Errorf("both %s are set; pass %s", strings.Join(vars, " and "), strings.Join(flags, " or "))
	case len(with) == 1:
		return with[0].Name, ids[with[0].Name], nil
	}
	for _, f := range d.Facts {
		if marked(environ, f) {
			return f.Name, "", nil
		}
	}
	products := joined(d.Facts, func(f agent.Descriptor) string { return f.Product })
	return "", "", errors.New("run duolog lead register inside a " + strings.Join(products, " or ") + " session")
}

// joined maps each descriptor to a string.
func joined(facts []agent.Descriptor, text func(agent.Descriptor) string) []string {
	out := make([]string, len(facts))
	for i, f := range facts {
		out[i] = text(f)
	}
	return out
}

// marked reports whether the environment carries a variable that only f's
// agent sets in its tool calls. The session variable is not one (a session
// without an id is a different error), and neither is an ENTRYPOINT variable,
// which says how a session was started, not that an agent runs the command.
// A variable is found as os.Getenv finds it: without case on Windows.
func marked(environ []string, f agent.Descriptor) bool {
	for _, m := range f.Markers {
		if !slices.Contains(f.SessionEnv, m) && !strings.HasSuffix(m, "_ENTRYPOINT") && launch.Getenv(environ, m) != "" {
			return true
		}
	}
	return false
}
