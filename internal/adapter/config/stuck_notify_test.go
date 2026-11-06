package config

import (
	"path/filepath"
	"testing"
)

func TestStuckNotifyMinutes(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil || cfg.Rex.StuckNotifyMinutes != 5 {
		t.Fatalf("%+v %v", cfg, err)
	}
	for _, value := range []struct {
		text string
		want int
		bad  bool
	}{{"0", 0, false}, {"2", 2, false}, {"-1", 0, true}} {
		writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "rex:\n  stuck_notify_minutes: "+value.text+"\n")
		cfg, err := Load(root)
		if value.bad {
			if err == nil {
				t.Fatal("negative notification delay accepted")
			}
			continue
		}
		if err != nil || cfg.Rex.StuckNotifyMinutes != value.want {
			t.Fatalf("%+v %v", cfg, err)
		}
	}
}
