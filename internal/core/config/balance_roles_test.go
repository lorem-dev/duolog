package config

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDefaultRolesBalance(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal(DefaultYAML(), &cfg); err != nil {
		t.Fatal(err)
	}
	for name, role := range cfg.Roles {
		want := name != "planner" && name != "review_cross"
		if got := role.Balanced(); got != want {
			t.Errorf("role %s: Balanced() = %v, want %v", name, got, want)
		}
	}
	rc, ok := cfg.Roles["review_cross"]
	if !ok {
		t.Fatal("review_cross missing")
	}
	if rc.Use != (Target{Agent: "codex", Model: "sol", Effort: "high"}) ||
		rc.Fallback != (Target{Agent: "claude", Model: "opus", Effort: "high"}) ||
		rc.Sandbox != "read-only" || rc.Context != "project" || rc.Session != "fresh" {
		t.Errorf("review_cross = %+v", rc)
	}
}
