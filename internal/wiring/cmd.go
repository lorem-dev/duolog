package wiring

import (
	"os"
	"runtime"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/core/budget"
	"github.com/lorem-dev/duolog/internal/gate"
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
	// AskWait turns policy.ask_timeout_seconds into the wait for an answer.
	AskWait func(seconds int) time.Duration
	// ObserveLead returns an observation of how agent's lead itself was started.
	ObserveLead func(agent string) (session.LeadRoute, bool)
	// AgentAncestor names a Claude or Codex process above this one.
	AgentAncestor func() string
	// AgentAbove reports whether the caller runs in an agent session.
	AgentAbove func(environ []string) bool
	// OpenLaunchState opens the account state a launcher records itself in.
	OpenLaunchState func() (*paidruns.Store, error)
	// IsTerminal reports whether v is a file open on a terminal.
	IsTerminal func(v any) bool
	// GOOS is the platform doctor describes.
	GOOS string
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
		// duolog has no source for an observation of the lead's start yet, and
		// its own environment never stands for the lead, so the lead reports
		// unknown.
		ObserveLead:     func(string) (session.LeadRoute, bool) { return session.LeadRoute{}, false },
		AgentAncestor:   AgentAncestor,
		AgentAbove:      session.AgentAbove,
		OpenLaunchState: func() (*paidruns.Store, error) { return paidruns.OpenUserState("") },
		IsTerminal:      isTerminal,
		GOOS:            runtime.GOOS,
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
