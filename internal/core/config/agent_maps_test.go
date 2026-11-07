package config

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestAgentMapsRoundTrip: the agent keys of paid_runs, coauthors and
// worker_modes decode into the maps and marshal back to the same YAML.
func TestAgentMapsRoundTrip(t *testing.T) {
	const text = `paid_runs:
  usable_share: 0.5
  claude:
    allow: true
    daily_cap_usd: 5
coauthors:
  enabled: true
  codex: X <x@example.invalid>
worker_modes:
  roles:
    review:
      codex: read-only
      network: true
      writable_roots: []
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	if a := cfg.PaidRuns.Agents["claude"]; !a.Allow || a.DailyCapUSD != 5 {
		t.Errorf("paid_runs.claude = %+v", a)
	}
	if cfg.Coauthors.Templates["codex"] != "X <x@example.invalid>" {
		t.Errorf("coauthors = %+v", cfg.Coauthors)
	}
	if m := cfg.WorkerModes.Roles["review"]; m.For("codex") != "read-only" || !m.Network {
		t.Errorf("worker_modes.roles.review = %+v", m)
	}
	out, err := yaml.Marshal(struct {
		PaidRuns    PaidRuns    `yaml:"paid_runs"`
		Coauthors   Coauthors   `yaml:"coauthors"`
		WorkerModes WorkerModes `yaml:"worker_modes"`
	}{cfg.PaidRuns, cfg.Coauthors, cfg.WorkerModes})
	if err != nil {
		t.Fatal(err)
	}
	var again Config
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("marshaled YAML does not decode: %v\n%s", err, out)
	}
	back, err := yaml.Marshal(struct {
		PaidRuns    PaidRuns    `yaml:"paid_runs"`
		Coauthors   Coauthors   `yaml:"coauthors"`
		WorkerModes WorkerModes `yaml:"worker_modes"`
	}{again.PaidRuns, again.Coauthors, again.WorkerModes})
	if err != nil || !reflect.DeepEqual(string(back), string(out)) {
		t.Errorf("round trip changed the YAML:\n%s\nthen\n%s (%v)", out, back, err)
	}
	// The keys are today's: agent names beside the section's own keys, and
	// nothing else.
	for _, want := range []string{"usable_share: 0.5", "claude:", "    allow: true", "    daily_cap_usd: 5", "codex: X <x@example.invalid>", "codex: read-only", "network: true"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("marshaled YAML lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "agents:") || strings.Contains(string(out), "templates:") || strings.Contains(string(out), "native:") {
		t.Errorf("marshaled YAML shows the Go maps:\n%s", out)
	}
}
