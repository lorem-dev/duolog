package doctortest

import (
	"testing"

	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

func TestProbeContract(t *testing.T) {
	ProbeContract(t, func(t *testing.T, present bool) (Probe, corelaunch.Process) {
		f := NewFake()
		f.Missing = !present
		return f, corelaunch.Process{}
	})
}
