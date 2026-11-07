package wiring

import (
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/rex"
)

// The recorded peer run lost an enter that followed a send at once: 0.7 s
// between them fixed it, so the commands keep at least that.
func TestPeerDepsDefaultPauses(t *testing.T) {
	d := PeerDeps(DelegateDeps())
	if d.Pauses.Send < 700*time.Millisecond {
		t.Fatalf("the default pause between send and enter is %v, want at least 700 ms", d.Pauses.Send)
	}
	if d.Clock == nil || len(d.Models) != len(Names()) || d.ResetTime == nil || d.Notify == nil || d.RecordPeer == nil || d.OnEnvFile == nil || d.Delegate == nil || d.PaidGate == nil || d.HeldGate == nil {
		t.Fatalf("a field of peer.Deps is empty: %+v", d)
	}
	if _, ok := d.Pane.(rex.Pane); !ok {
		t.Fatalf("peer.Deps.Pane is %T, want rex.Pane", d.Pane)
	}
	if p := ProfileDeps(DelegateDeps()); p.Delegate == nil || p.Rand == nil || p.RunCheck == nil {
		t.Fatalf("a field of profile.Deps is empty: %+v", p)
	}
	if o := DoctorDeps(DelegateDeps()); o.PaidGate == nil || o.GateBinary == nil || o.Available == nil || len(o.Probes) != len(Agents()) {
		t.Fatalf("a field of doctor.Deps is empty: %+v", o)
	}
}
