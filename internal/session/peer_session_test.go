package session

import "testing"

func TestPeerRexSessionSurvivesRegistryReopen(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := r.PeerRexSession(); err != nil || id != "" {
		t.Fatalf("%q %v", id, err)
	}
	if err := r.SetPeerRexSession("session:peers"); err != nil {
		t.Fatal(err)
	}
	other, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := other.PeerRexSession(); err != nil || id != "session:peers" {
		t.Fatalf("%q %v", id, err)
	}
	if err := other.SetPeerRexSession(""); err != nil {
		t.Fatal(err)
	}
	if id, err := r.PeerRexSession(); err != nil || id != "" {
		t.Fatalf("%q %v", id, err)
	}
}
