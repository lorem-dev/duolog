package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFreshSessionHintLayers(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil || cfg.Lead.FreshSessionHint != 200000 {
		t.Fatalf("default: %+v, %v", cfg.Lead, err)
	}

	// The person's layers may set any count, 0 included.
	writeConfig(t, userConfigPath(t), "lead: {fresh_session_hint: 0}\n")
	if cfg, err = Load(root); err != nil || cfg.Lead.FreshSessionHint != 0 || len(cfg.Notices) != 0 {
		t.Errorf("user: %+v, %v, notices %v", cfg.Lead, err, cfg.Notices)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "lead: {fresh_session_hint: 300000}\n")
	if cfg, err = Load(root); err != nil || cfg.Lead.FreshSessionHint != 300000 {
		t.Errorf("local: %+v, %v", cfg.Lead, err)
	}

	// The shared project may not set it: the value is ignored and named.
	for _, n := range []string{"0", "1000", "900000"} {
		root = isolatedRoot(t)
		writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "lead: {fresh_session_hint: "+n+"}\n")
		if cfg, err = Load(root); err != nil || cfg.Lead.FreshSessionHint != 200000 || !slices.Equal(noticeKeys(cfg.Notices), []string{"lead.fresh_session_hint"}) {
			t.Errorf("project %s: %+v, %v, notices %v", n, cfg.Lead, err, cfg.Notices)
		}
	}

	for _, y := range []string{"lead: {fresh_session_hint: -1}\n", "lead: {fresh_session_hint: 1.5}\n"} {
		root = isolatedRoot(t)
		writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), y)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "lead.fresh_session_hint") {
			t.Errorf("%q: err = %v", y, err)
		}
	}
}
