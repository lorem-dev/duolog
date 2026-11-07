// Package agent holds the facts about one coding agent as a plain value. Facts
// do not run, so there is nothing to fake; each agent's adapter exports one.
package agent

// EnvNames groups the credential variables of an agent's provider.
type EnvNames struct {
	APIKey, AuthToken, BaseURL []string
}

// ComposerFacts describes where an agent draws its input line.
type ComposerFacts struct {
	// Marker starts the input line: "›" for Codex, "❯" for Claude. An agent
	// with no marker has no composer to find.
	Marker string
	// Boxed means the line sits between two rules, which keeps it apart from a
	// menu cursor, and the rows of a wrapped input end at the rule below.
	Boxed bool
}

// AutoAnswer is the option an agent's pane picks for a gate verdict on a
// question it asks, by the option's label.
type AutoAnswer struct {
	// Verdict is "allow" or "deny".
	Verdict string
	Label   string
}

// PaneFacts holds the texts of an agent's terminal pane, as the screen reader
// takes them. The zero value is an agent with no pane support.
type PaneFacts struct {
	// Placeholder is a regular expression for the input line's placeholder
	// text, matched against the text after the marker.
	Placeholder string
	// Updater holds lower-case phrases that show the agent updating itself.
	Updater []string
	// Interrupt holds regular expressions for the notice above the input line
	// after a turn was interrupted.
	Interrupt []string
	Composer  ComposerFacts
	// Answers are the labels the pane picks for a gate verdict.
	Answers []AutoAnswer
	// CommandQuestion is the fixed prompt shown above a command the agent wants
	// to run; empty when it has none.
	CommandQuestion string
	// Queued is the hint of a question behind the agent's follow-up input
	// panel; empty when it has none.
	Queued string
}

// ConfigFacts names the keys of the config that only some agents have.
type ConfigFacts struct {
	SubscriptionEndpoints, SandboxRoots bool
	// SkillsDir is the default project skills folder of an agent whose folder
	// agents.<name>.skills_dir sets; empty means the agent has no such key.
	SkillsDir string
	// CoauthorDoc is the schema text of coauthors.<agent>.
	CoauthorDoc string
	// NativeModes are the native names a worker may start in, lowest first,
	// and NativeModeDoc is the schema text of worker_modes.*.<agent>.
	NativeModes   []string
	NativeModeDoc string
}

// Descriptor is everything the code knows about one agent as data.
type Descriptor struct {
	Name, Display   string   // "claude", "Claude"
	Product         string   // what the person runs: "Claude Code", "Codex"
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
