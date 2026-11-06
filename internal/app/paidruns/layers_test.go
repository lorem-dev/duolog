package paidruns

import (
	"context"
	"runtime"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A launch whose home, platform or, for a worker or a peer, project root is
// unknown has settings layers duolog cannot find: they count as unreadable,
// a paid signal, so the free variant is refused rather than run unchecked.
func TestReadLayersUnknownLocationsAreUnreadable(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	full := workerenv.Locations{Home: home, Root: root, GOOS: runtime.GOOS}
	for _, c := range []struct {
		name   string
		shape  workerenv.Shape
		loc    workerenv.Locations
		refuse bool
	}{
		{"all known", workerenv.Peer, full, false},
		{"empty", workerenv.Peer, workerenv.Locations{}, true},
		{"no home", workerenv.Peer, workerenv.Locations{Root: root, GOOS: runtime.GOOS}, true},
		{"no platform", workerenv.Worker, workerenv.Locations{Home: home, Root: root}, true},
		{"peer without root", workerenv.Peer, workerenv.Locations{Home: home, GOOS: runtime.GOOS}, true},
		{"worker without root", workerenv.Worker, workerenv.Locations{Home: home, GOOS: runtime.GOOS}, true},
	} {
		for _, agent := range []string{"claude", "codex"} {
			t.Run(c.name+"/"+agent, func(t *testing.T) {
				in := workerenv.ProcessInput{Agent: agent, Shape: c.shape, Variant: workerenv.Free, Config: &config.Config{}, Locations: c.loc}
				facts, err := ReadLayers(context.Background(), in)
				if err != nil {
					t.Fatal(err)
				}
				unread := slices.ContainsFunc(facts, func(f workerenv.LayerFacts) bool { return f.Unreadable })
				if unread != c.refuse {
					t.Fatalf("unreadable %v, want %v: %+v", unread, c.refuse, facts)
				}
			})
		}
	}
}
