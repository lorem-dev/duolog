package wiring

import (
	"encoding/json"
	"slices"
	"testing"
)

// A worker never runs in fast mode, which draws on usage credits even inside a
// subscription window. The pin holds for fresh and resumed runs alike.
func TestWorkerFastModePinned(t *testing.T) {
	for _, thread := range []string{"", "thread-1"} {
		for _, context := range []string{"minimal", "project"} {
			s := spec(t, "claude")
			s.ThreadID, s.Route.Context = thread, context
			args := Argv(s)
			if thread != "" && !slices.Contains(args, "--resume") {
				t.Fatal(args)
			}
			i := slices.Index(args, "--settings")
			if i < 0 || i+1 == len(args) {
				t.Fatal(args)
			}
			var settings struct {
				FastMode          *bool `json:"fastMode"`
				AutoMemoryEnabled *bool `json:"autoMemoryEnabled"`
				Sandbox           struct {
					Filesystem struct {
						DenyRead []string `json:"denyRead"`
					} `json:"filesystem"`
				} `json:"sandbox"`
			}
			if err := json.Unmarshal([]byte(args[i+1]), &settings); err != nil {
				t.Fatal(err)
			}
			if settings.FastMode == nil || *settings.FastMode {
				t.Fatal(thread, context, args[i+1])
			}
			if settings.AutoMemoryEnabled == nil || *settings.AutoMemoryEnabled || len(settings.Sandbox.Filesystem.DenyRead) == 0 {
				t.Fatal(thread, context, args[i+1])
			}
			if !slices.Contains(args, heldDisallowed) {
				t.Fatal(args)
			}
		}
	}
}
