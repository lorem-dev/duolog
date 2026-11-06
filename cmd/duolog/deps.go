package main

import (
	"context"
	"crypto/rand"
	"os"

	calibration "github.com/lorem-dev/duolog/internal/app/calibrate"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/spf13/cobra"
)

// cmdDeps is everything the commands reach outside this package: main builds
// it once and every command constructor takes it. Tests build it with
// wiringtest.Cmd and change a field.
//
// The fields that name types of the calibrate package sit here, not in
// wiring: calibrate's tests import wiring, and wiring would import calibrate
// back.
type cmdDeps struct {
	wiring.CmdDeps

	// QuotaReader replaces calibration's quota reads in tests; nil reads them
	// through the account gate.
	QuotaReader calibration.QuotaReader
	// SurveyChecks runs the baseline checks, CellChecks the class checks of a
	// calibration cell; both run the check process.
	SurveyChecks, CellChecks calibration.CheckRunner
	// MineChecks runs the class checks that validate mined tasks, and MineGH
	// the gh CLI for pull request texts.
	MineChecks calibration.CheckRunner
	MineGH     calibration.GH
}

// newCmdDeps wraps the dependencies of the commands that wiring built.
func newCmdDeps(w wiring.CmdDeps) *cmdDeps {
	d := &cmdDeps{CmdDeps: w, MineGH: runGH}
	d.SurveyChecks, d.CellChecks, d.MineChecks = d.profileChecks, d.profileChecks, d.profileChecks
	return d
}

// runDelegate runs a delegation: RunDelegate when one is set, else delegate.Run
// on d.Delegate as it is now.
func (d *cmdDeps) runDelegate(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
	if d.RunDelegate != nil {
		return d.RunDelegate(ctx, r)
	}
	return delegate.Run(ctx, d.Delegate, r)
}

// The Deps of the app packages are built when a command runs, so that they read
// the Delegate and RunDelegate as the test of the moment has set them.

func (d *cmdDeps) peerDeps() peer.Deps { return d.PeerDeps(d.Delegate) }

func (d *cmdDeps) doctorDeps() doctor.Deps { return d.DoctorDeps(d.Delegate) }

func (d *cmdDeps) profileDeps() profile.Deps {
	p := d.ProfileDeps(d.Delegate)
	p.Delegate = d.runDelegate
	return p
}

// calibrateDeps is built here, not in wiring: see cmdDeps.
func (d *cmdDeps) calibrateDeps() calibration.Deps {
	return calibration.Deps{
		Delegate: d.runDelegate,
		Verify:   calibration.VerifyIsolated,
		Rand:     coin,
	}
}

// coin is true or false with equal chance.
func coin() bool {
	var bit [1]byte
	if _, err := rand.Read(bit[:]); err != nil {
		panic(err)
	}
	return bit[0]&1 == 1
}

// profileChecks is profile.RunChecks on the real check process.
func (d *cmdDeps) profileChecks(ctx context.Context, dir string, commands []string) []journal.Check {
	return profile.RunChecks(ctx, d.profileDeps(), dir, commands)
}

// modelContext reports whether a model may read this command's output: a
// worker, a lead's tool call, or output that does not go to a terminal. No
// flag can claim the person's terminal.
func (d *cmdDeps) modelContext(cmd *cobra.Command) bool {
	if isWorker() {
		return true
	}
	for _, name := range modelMarkers {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return !d.IsTerminal(cmd.OutOrStdout())
}
