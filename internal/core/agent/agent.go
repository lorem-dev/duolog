// Package agent holds the facts about one coding agent as a plain value. Facts
// do not run, so there is nothing to fake; each agent's adapter exports one.
package agent

// EnvNames groups the credential variables of an agent's provider.
type EnvNames struct {
	APIKey, AuthToken, BaseURL []string
}

// PaneFacts holds the texts of an agent's terminal pane. It is filled in when
// the pane port lands.
type PaneFacts struct{}

// ConfigFacts names the keys of the config that only some agents have.
type ConfigFacts struct {
	SubscriptionEndpoints, SandboxRoots bool
}

// Descriptor is everything the code knows about one agent as data.
type Descriptor struct {
	Name, Display   string   // "claude", "Claude"
	Binaries        []string // process names: claude; codex, codex-*
	Helpers         []string // sandbox helper processes
	ConfigEnv       string   // CLAUDE_CONFIG_DIR, CODEX_HOME
	ConfigDefault   string   // .claude, .codex under home
	ProjectDir      string   // .claude, .codex
	SessionEnv      []string // CLAUDE_CODE_SESSION_ID, CODEX_THREAD_ID
	Markers         []string // variables the agent sets in its tool calls
	ModelPrefix     string   // "claude-", "gpt-"
	Families        []string // aliases for telemetry and quota windows
	Credentials     EnvNames // credential variable names
	HiddenPrefixes  []string // env prefixes the MCP bridge hides
	WorkerEnv       []string // entries added to a worker's environment
	LoginPhrases    []string // lower-case texts of a refused login
	InstructionFile string   // CLAUDE.md, AGENTS.md
	LeadCanAsk      bool     // the lead can ask the person
	Pane            PaneFacts
	Config          ConfigFacts
}
