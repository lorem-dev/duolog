package wiring

import (
	"os"
	"runtime"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/app/quota"
	agentsync "github.com/lorem-dev/duolog/internal/app/sync"
	"github.com/lorem-dev/duolog/internal/core/budget"
	"github.com/lorem-dev/duolog/internal/rex"
)

// CmdDeps is what the commands of cmd/duolog reach outside their package:
// one field for each function or value that a test of a command replaces.
// cmd/duolog adds the fields that name types of the calibrate package, which
// wiring cannot import: calibrate's tests import wiring.
type CmdDeps struct {
	// Delegate is what delegate.Run reaches. The Deps of the other app packages
	// are built from it when a command runs, so a field a test replaces counts.
	Delegate delegate.Deps
	// RunDelegate runs a delegation; nil runs delegate.Run on Delegate.
	RunDelegate delegate.Runner
	// PeerDeps, ProfileDeps and DoctorDeps build the Deps of their app package
	// from Delegate when a command runs, so that a test changes a field of
	// Delegate or of what they build.
	PeerDeps    func(delegate.Deps) peer.Deps
	ProfileDeps func(delegate.Deps) profile.Deps
	DoctorDeps  func(delegate.Deps) doctor.Deps

	// Install is what install and uninstall reach: the registered agents'
	// Installers.
	Install install.Deps

	// CalibrationNext names the calibrate command after each step.
	CalibrationNext map[string]string
	// LocateRex finds the rex that doctor asks for the pane inventory.
	LocateRex func() (string, bool)
	// Lead is what lead register reaches: each agent's Lead, the registry and
	// the agent process above the command's shell.
	Lead lead.Deps
	// AskWait turns the configured seconds of an ask into the wait for an
	// answer, given the timeout of the hook that runs the gate.
	AskWait func(seconds int, person bool, hook time.Duration) time.Duration
	// HookLimit reads the timeout of the gate hook that runs for agent and event.
	HookLimit func(agent, event, root, work string, environ []string) time.Duration
	// ObserveLead returns an observation of how agent's lead itself was started.
	ObserveLead func(agent string) (session.LeadRoute, bool)
	// AgentAncestor names a Claude or Codex process above this one.
	AgentAncestor func() string
	// ParentAgent names a Claude or Codex process that is this one's direct
	// parent.
	ParentAgent func() string
	// AgentAbove reports whether the caller runs in an agent session.
	AgentAbove func(environ []string) bool
	// OpenLaunchState opens the account state a launcher records itself in.
	OpenLaunchState func() (*paidruns.Store, error)
	// IsTerminal reports whether v is a file open on a terminal.
	IsTerminal func(v any) bool
	// GOOS is the platform doctor describes.
	GOOS string
	// SyncFormats holds the format of each agent that duolog sync converts.
	SyncFormats agentsync.Formats
	// Quota is what quota reads with: each agent's reader and, in tests, the
	// reader that replaces calibration's reads. The readers carry no identity
	// sink, so only Calibration reads through them; they must never read an
	// account.
	Quota quota.Deps
	// Notify shows the person a desktop notice: an ask only they can answer,
	// and a detached run that ended needing them. It must not block or fail
	// its caller.
	Notify func(title, message string)
}

// Cmd fills every field of CmdDeps with the real function.
func Cmd() CmdDeps {
	return CmdDeps{
		Delegate:        DelegateDeps(),
		PeerDeps:        PeerDeps,
		ProfileDeps:     ProfileDeps,
		DoctorDeps:      DoctorDeps,
		Install:         InstallDeps(),
		CalibrationNext: budget.NextStep,
		LocateRex:       func() (string, bool) { return rex.Locate(os.Getenv("REX_BIN_DIR"), rex.BundleGlobs()) },
		Lead:            LeadDeps(),
		AskWait:         gate.AskWait,
		HookLimit:       HookAskLimit,
		// duolog has no source for an observation of the lead's start yet, and
		// its own environment never stands for the lead, so the lead reports
		// unknown.
		ObserveLead:     func(string) (session.LeadRoute, bool) { return session.LeadRoute{}, false },
		AgentAncestor:   AgentAncestor,
		ParentAgent:     ParentAgent,
		AgentAbove:      session.AgentAbove,
		OpenLaunchState: func() (*paidruns.Store, error) { return paidruns.OpenUserState("") },
		IsTerminal:      isTerminal,
		GOOS:            runtime.GOOS,
		SyncFormats:     SyncFormats(),
		Quota:           QuotaDeps(nil),
		Notify:          kit.Notify,
	}
}

// isTerminal reports whether v is a file open on a terminal.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
