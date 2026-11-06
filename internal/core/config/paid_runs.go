package config

// PaidAgent is one agent's paid-run opt-in. Paid runs are off by default;
// allow needs a positive daily cap, and calibration on a paid route needs
// calibrate as well.
type PaidAgent struct {
	Allow       bool     `yaml:"allow" jsonschema_description:"Allow paid runs for this agent's workers and peers once the subscription cannot serve them; defaults to false. Needs daily_cap_usd above 0."`
	DailyCapUSD float64  `yaml:"daily_cap_usd" jsonschema_description:"Most USD paid runs of this agent may spend in the last 24 hours; defaults to 0 and must be above 0 with allow." jsonschema:"minimum=0"`
	Calibrate   bool     `yaml:"calibrate" jsonschema_description:"Allow calibration on a paid route; defaults to false and needs allow."`
	Env         []string `yaml:"env" jsonschema_description:"Variables passed to this agent's processes only when paid runs are allowed; defaults to the agent's API key."`
	Account     string   `yaml:"account" jsonschema_description:"Account kind: workspace uses usable_share of each window; auto and personal use whole windows only while the quota read and the runs show no workspace evidence, which always wins; defaults to auto." jsonschema:"enum=auto,enum=personal,enum=workspace"`
}

type PaidClaude struct {
	PaidAgent             `yaml:",inline"`
	SubscriptionEndpoints map[string]string `yaml:"subscription_endpoints" jsonschema_description:"Named base URLs that count as the subscription, such as a gateway in front of the claude.ai login; defaults to an empty map."`
}

type PaidRuns struct {
	UsableShare float64    `yaml:"usable_share" jsonschema_description:"Share of each window duolog uses on a workspace account, above 0 and at most 1; defaults to 0.5." jsonschema:"exclusiveMinimum=0,maximum=1"`
	Claude      PaidClaude `yaml:"claude" jsonschema_description:"Paid runs of Claude workers and peers; off by default."`
	Codex       PaidAgent  `yaml:"codex" jsonschema_description:"Paid runs of Codex workers and peers; off by default."`
}

// ConfigNotice names a key that Load ignored or capped, and why: a key of a
// shared layer, .duolog/config.yaml or a .duolog/config.local.yaml that Git
// tracks, that would widen a limit or lower a price, or a .duolog layer, shared
// or local, that sets the total or a session tier above its default, which
// only the user config may do.
type ConfigNotice struct {
	Key, Reason string
}

// Agent returns the settings of claude or codex, and the zero value, which
// allows nothing, for any other name.
func (p PaidRuns) Agent(agent string) PaidAgent {
	switch agent {
	case "claude":
		return p.Claude.PaidAgent
	case "codex":
		return p.Codex
	}
	return PaidAgent{}
}
