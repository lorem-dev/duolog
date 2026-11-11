package config

import (
	"path/filepath"
	"slices"
	"testing"
)

// lead.notify defaults to true. Only the person's own layers may set it: a
// shared project that turned it off would silence every teammate's alerts.
func TestLeadNotify(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil || !cfg.Lead.Notifies() {
		t.Fatalf("default: %+v, %v", cfg.Lead, err)
	}

	writeConfig(t, userConfigPath(t), "lead: {notify: false}\n")
	if cfg, err = Load(root); err != nil || cfg.Lead.Notifies() || len(cfg.Notices) != 0 {
		t.Fatalf("user false: %+v, %v, notices %v", cfg.Lead, err, cfg.Notices)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "lead: {notify: true}\n")
	if cfg, err = Load(root); err != nil || !cfg.Lead.Notifies() {
		t.Fatalf("local true: %+v, %v", cfg.Lead, err)
	}

	// The shared project may not set it: the value is ignored and named.
	root = isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "lead: {notify: false}\n")
	if cfg, err = Load(root); err != nil || !cfg.Lead.Notifies() || !slices.Equal(noticeKeys(cfg.Notices), []string{"lead.notify"}) {
		t.Fatalf("project false: %+v, %v, notices %v", cfg.Lead, err, cfg.Notices)
	}
}
