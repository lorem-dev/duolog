package calibration

import "github.com/lorem-dev/duolog/internal/app/calibrate/calibratetest"

// fakeTrees are tree guards of both agents that pass every tree they
// prepared, as the real ones do for a tree the person trusts. The cell and
// judge tests on the real guards and the real delegation are in
// internal/wiring.
func fakeTrees() Trees {
	return Trees{"claude": &calibratetest.Fake{}, "codex": &calibratetest.Fake{}}
}
