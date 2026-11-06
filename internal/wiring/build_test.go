package wiring

import (
	"testing"
	"time"
)

// The recorded peer run lost an enter that followed a send at once: 0.7 s
// between them fixed it, so the commands keep at least that.
func TestPeerDepsDefaultPauses(t *testing.T) {
	d := PeerDeps(DelegateDeps())
	if d.Pauses.Send < 700*time.Millisecond {
		t.Fatalf("the default pause between send and enter is %v, want at least 700 ms", d.Pauses.Send)
	}
	if d.Clock == nil || d.ResolveModel == nil || d.Notify == nil || d.RecordPeer == nil || d.OnEnvFile == nil || d.Delegate == nil || d.PaidGate == nil || d.HeldGate == nil {
		t.Fatalf("a field of peer.Deps is empty: %+v", d)
	}
	if p := ProfileDeps(DelegateDeps()); p.Delegate == nil || p.Rand == nil || p.RunCheck == nil {
		t.Fatalf("a field of profile.Deps is empty: %+v", p)
	}
	if o := DoctorDeps(DelegateDeps()); o.PaidGate == nil || o.GateBinary == nil {
		t.Fatalf("a field of doctor.Deps is empty: %+v", o)
	}
}
