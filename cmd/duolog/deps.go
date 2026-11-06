package main

import (
	"context"
	"crypto/rand"

	calibration "github.com/lorem-dev/duolog/internal/app/calibrate"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// The Deps of the app packages, built when a command runs so that they read
// delegateDeps and runDelegate as the test of the moment has set them. Tests
// replace a variable to change a field.
var (
	peerDeps = func() peer.Deps { return wiring.PeerDeps(delegateDeps) }

	doctorDeps = func() doctor.Deps { return wiring.DoctorDeps(delegateDeps) }

	profileDeps = func() profile.Deps {
		d := wiring.ProfileDeps(delegateDeps)
		d.Delegate = runDelegate
		return d
	}

	// calibrateDeps is built here, not in wiring: the calibrate tests import
	// wiring for delegate's Deps, and wiring would import calibrate back.
	calibrateDeps = func() calibration.Deps {
		return calibration.Deps{
			Delegate: runDelegate,
			Verify:   calibration.VerifyIsolated,
			Rand:     coin,
		}
	}
)

// coin is true or false with equal chance.
func coin() bool {
	var bit [1]byte
	if _, err := rand.Read(bit[:]); err != nil {
		panic(err)
	}
	return bit[0]&1 == 1
}

// profileChecks is profile.RunChecks on the real check process.
func profileChecks(ctx context.Context, dir string, commands []string) []journal.Check {
	return profile.RunChecks(ctx, profileDeps(), dir, commands)
}
