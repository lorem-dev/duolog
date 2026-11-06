package wiring

import (
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// TestIsWorkerTwin pins the app tier's copy of IsWorker to the boundary's.
func TestIsWorkerTwin(t *testing.T) {
	for _, v := range []struct {
		name  string
		set   bool
		value string
	}{{"unset", false, ""}, {"empty", true, ""}, {"zero", true, "0"}, {"one", true, "1"}, {"two", true, "2"}, {"x", true, "x"}} {
		t.Run(v.name, func(t *testing.T) {
			t.Setenv("DUOLOG_DEPTH", v.value)
			if !v.set {
				if err := os.Unsetenv("DUOLOG_DEPTH"); err != nil {
					t.Fatal(err)
				}
			}
			if got, want := launch.IsWorker(), workerenv.IsWorker(); got != want {
				t.Errorf("launch.IsWorker() = %v, workerenv.IsWorker() = %v", got, want)
			}
		})
	}
}
