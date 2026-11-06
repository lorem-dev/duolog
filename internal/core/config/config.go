package config

import (
	"slices"
	"strconv"
	"strings"
)

type Config struct {
	SkillTests     string           `yaml:"skill_tests" jsonschema_description:"Cross-model skill testing preference: ask before each change, always test, or never test; defaults to ask." jsonschema:"enum=ask,enum=always,enum=never"`
	Coauthors      Coauthors        `yaml:"coauthors" jsonschema_description:"Commit co-author templates for workers; enabled by default."`
	Version        int              `yaml:"version" jsonschema_description:"Configuration format version; defaults to 1."`
	Wire           Wire             `yaml:"wire" jsonschema_description:"Task and message encoding and size limits; defaults suit the file bus."`
	Limits         Limits           `yaml:"limits" jsonschema_description:"Worker and session limits: resumable sessions per lead, live sessions per account by measured tier and in all, and the slot wait."`
	Models         map[string]Model `yaml:"models" jsonschema_description:"Model aliases and their prices, which drive estimates and admission of paid runs; defaults include the eight vendor families."`
	Roles          map[string]Role  `yaml:"roles" jsonschema_description:"Named task routes; overrides merge by role and field into the eleven defaults."`
	Sessions       Sessions         `yaml:"sessions" jsonschema_description:"Thread lifetime limits; defaults close large or idle threads."`
	Policy         Policy           `yaml:"policy" jsonschema_description:"Permission rules constrained by the lead; defaults ask for unmatched actions."`
	Capabilities   Capabilities     `yaml:"capabilities" jsonschema_description:"Missing capability handling; defaults read skills and let the lead handle tools."`
	Sync           Sync             `yaml:"sync" jsonschema_description:"Settings for generating agent and skill files; automatic sync defaults to true."`
	EnvPassthrough []string         `yaml:"env_passthrough" jsonschema_description:"Environment variables passed to workers; defaults include native OS login and agent config directories, not API keys. A free run drops names that bill per token even when listed; only paid_runs.<agent>.allow lets them through."`
	Workers        Workers          `yaml:"workers" jsonschema_description:"What Claude workers and peers inherit from the person's own settings."`
	Rex            Rex              `yaml:"rex" jsonschema_description:"Terminal capture and startup settings; defaults to 20 lines and 120 seconds."`
	Learning       Learning         `yaml:"learning" jsonschema_description:"Learning mode: task classes, classification rules, ladders and priors; off by default."`
	Telemetry      Telemetry        `yaml:"telemetry" jsonschema_description:"Local telemetry that never leaves the machine; on by default. User level, except that a project may turn off its own events."`
	Calibration    Calibration      `yaml:"calibration" jsonschema_description:"Project calibration caps and task filters; calibration runs only when the person asks the lead for it."`
	WorkerModes    WorkerModes      `yaml:"worker_modes" jsonschema_description:"Native permission mode a delegate worker starts in, per role and model family; never above the lead's mode. User level, project layers may only lower. Absent means duolog's default flags."`
	PaidRuns       PaidRuns         `yaml:"paid_runs" jsonschema_description:"Paid runs per agent with a daily USD cap, and the share of each window used on workspace accounts; paid runs are off by default."`
	Balance        Balance          `yaml:"balance" jsonschema_description:"How quota reads and learned window rates are trusted: read age, interval size and the margin of a tier step up. The shared project config may only make these stricter."`
	Routing        Routing          `yaml:"routing" jsonschema_description:"How a new task picks between a role's agents beyond the ladder: balance subscriptions by pace, and the margin. The shared project config may only tighten these."`
	// Notices names the keys that Load ignored or capped: values of a shared
	// layer, .duolog/config.yaml or a .duolog/config.local.yaml that Git
	// tracks, that would widen paid runs or session limits or lower model
	// prices, and session tiers above their defaults.
	Notices []ConfigNotice `yaml:"-" json:"-" jsonschema:"-"`
}

type Workers struct {
	InheritPlugins bool `yaml:"inherit_plugins" jsonschema_description:"Let Claude workers and peers load every plugin enabled in the user and project settings; defaults to false, which turns them off except duolog's own, since each adds tokens to every request."`
	CompactWindow  string         `yaml:"compact_window" jsonschema_description:"Context size in tokens at which Claude workers and peers compact, set as CLAUDE_CODE_AUTO_COMPACT_WINDOW in their settings. auto, the default, takes it from compact_ladder by the Claude plan read at worker start; 0 leaves Claude's own default; otherwise 100000 to 1000000. The lead and Codex workers are not changed."`
	CompactLadder  map[string]int `yaml:"compact_ladder" jsonschema_description:"The window compact_window auto picks, by Claude plan (free, pro, max, ultra, team, business, enterprise or edu). Each value is 0 or 100000 to 1000000; a plan that is not listed gets 0, which leaves Claude's own default."`
}

// Window is the compact window in tokens for a Claude worker or peer on plan,
// 0 for none. An unset compact_window and an unknown plan give 0. The value is
// the one Load has checked.
func (w Workers) Window(plan string) int {
	if w.CompactWindow == "auto" {
		return w.CompactLadder[plan]
	}
	n, _ := strconv.Atoi(w.CompactWindow)
	return n
}

// ValidCompactWindow reports whether n is 0 or within the compact window bounds.
func ValidCompactWindow(n int) bool {
	return n == 0 || n >= MinCompactWindow && n <= MaxCompactWindow
}

// Compact window bounds in tokens, for workers.compact_window.
const (
	MinCompactWindow = 100000
	MaxCompactWindow = 1000000
)

type Coauthors struct {
	Enabled bool   `yaml:"enabled" jsonschema_description:"Record worker changes and offer commit co-author lines; defaults to true."`
	Claude  string `yaml:"claude" jsonschema_description:"Claude co-author template with {name} and {model}; defaults to a display name and noreply@anthropic.com."`
	Codex   string `yaml:"codex" jsonschema_description:"Codex co-author template with {name} and {model}; defaults to OpenAI Codex, the model and noreply@openai.com."`
}

type Target struct {
	Agent  string `yaml:"agent" jsonschema_description:"Agent that runs this target; the default depends on the role." jsonschema:"enum=claude,enum=codex"`
	Model  string `yaml:"model" jsonschema_description:"Model alias from models, or a deliberate gpt- or claude- version pin; defaults by role."`
	Effort string `yaml:"effort" jsonschema_description:"Requested reasoning effort; defaults by role and is capped by the lead when routed." jsonschema:"enum=low,enum=medium,enum=high,enum=xhigh,enum=max,enum=ultra"`
}

type Role struct {
	Use      Target `yaml:"use" jsonschema_description:"Preferred agent, model and effort; defaults depend on the role."`
	Fallback Target `yaml:"fallback" jsonschema_description:"Alternative target when the preferred agent is unavailable; defaults use the other vendor."`
	Sandbox  string `yaml:"sandbox" jsonschema_description:"Maximum file access for the role; defaults to read-only for reviews and workspace-write for edits." jsonschema:"enum=read-only,enum=workspace-write"`
	Context  string `yaml:"context" jsonschema_description:"Project instruction scope; defaults to project for code work and minimal for small tasks." jsonschema:"enum=minimal,enum=project"`
	Session  string `yaml:"session" jsonschema_description:"Thread reuse policy; defaults to fresh except plan, debug and debug_hard, which resume." jsonschema:"enum=fresh,enum=resume"`
}

type Wire struct {
	MaxSummaryBytes int `yaml:"max_summary_bytes" jsonschema_description:"Maximum summary size in bytes; defaults to 200."`
	MaxLineBytes    int `yaml:"max_line_bytes" jsonschema_description:"Maximum bus message size in bytes; defaults to 512."`
	InlineMaxBytes  int `yaml:"inline_max_bytes" jsonschema_description:"Maximum task and Read file bytes sent inline to worker stdin; defaults to 65536."`
}

type Limits struct {
	ManualOnlyModels []string `yaml:"manual_only_models" jsonschema_description:"Model aliases requiring explicit manual permission; defaults to fable and astra."`
	MaxLiveSessions  int      `yaml:"max_live_sessions" jsonschema_description:"Maximum live resumable sessions per lead and agent; defaults to 1 and excludes fresh runs. The account session limits apply too, and the lower one wins."`
	SelfMaxReadFiles int      `yaml:"self_max_read_files" jsonschema_description:"Maximum files for work handled by the lead on a provider limit; defaults to 3."`
	SelfMaxReadLines int      `yaml:"self_max_read_lines" jsonschema_description:"Maximum lines for work handled by the lead on a provider limit; defaults to 200."`
	// The account session limits count running workers and open peer panes
	// of every lead and project together.
	MaxTotalSessions   int          `yaml:"max_total_sessions" jsonschema_description:"Most live sessions of all agents together: running workers and open peer panes of every lead and project; defaults to 10. The shared project config may only lower it." jsonschema:"minimum=1"`
	SessionsByTier     SessionTiers `yaml:"sessions_by_tier" jsonschema_description:"Most live sessions per agent by its measured subscription tier; defaults to 3, 5 and 10. Only the user config may raise a tier above its default; every other config may only lower it. Raising one tier does not raise the next."`
	SessionWaitSeconds int          `yaml:"session_wait_seconds" jsonschema_description:"Seconds a delegation waits for a free session slot, first come first served; defaults to 60. The shared project config may only lower it." jsonschema:"minimum=0"`
}

type Model struct {
	Agent     string  `yaml:"agent" jsonschema_description:"Vendor agent for this alias; defaults to claude or codex according to the family." jsonschema:"enum=claude,enum=codex"`
	Family    string  `yaml:"family" jsonschema_description:"Codex model family used to resolve the newest visible version; absent for Claude aliases."`
	PricedFor string  `yaml:"priced_for" jsonschema_description:"Version whose prices are recorded; the prices drive estimates and admission of paid runs against the daily cap."`
	Input     float64 `yaml:"input" jsonschema_description:"Input price in USD per million tokens; defaults to the recorded vendor price."`
	Cached    float64 `yaml:"cached" jsonschema_description:"Cached input price in USD per million tokens; defaults to the recorded vendor price."`
	Output    float64 `yaml:"output" jsonschema_description:"Output price in USD per million tokens; defaults to the recorded vendor price."`
	Note      string  `yaml:"note" jsonschema_description:"Optional price caveat shown with estimates; empty unless the vendor has a special rate."`
}

type Sessions struct {
	ResumeMaxInputTokens int `yaml:"resume_max_input_tokens" jsonschema_description:"Cumulative input tokens before starting a new thread; defaults to 150000."`
	IdleCloseMinutes     int `yaml:"idle_close_minutes" jsonschema_description:"Minutes of inactivity before closing a thread; defaults to 30."`
}

type Policy struct {
	OnUnmatched       string `yaml:"on_unmatched" jsonschema_description:"Decision for an action with no matching rule; defaults to ask." jsonschema:"enum=allow,enum=deny,enum=ask"`
	AskTimeoutSeconds int    `yaml:"ask_timeout_seconds" jsonschema_description:"Seconds to wait for a permission answer; defaults to 120."`
	Rules             []Rule `yaml:"rules" jsonschema_description:"Permission rules for actions and paths; defaults allow routine work and deny sensitive operations."`
}

type Rule struct {
	Action   string   `yaml:"action" jsonschema_description:"Action type matched by this rule, such as read, write, exec, net or mcp (match server/tool)."`
	Match    string   `yaml:"match" jsonschema_description:"Glob expression selecting paths or commands; ** matches all paths."`
	Decision string   `yaml:"decision" jsonschema_description:"Permission result for a matching rule; deny takes precedence over allow." jsonschema:"enum=allow,enum=deny,enum=ask"`
	Roles    []string `yaml:"roles" jsonschema_description:"Roles to which the rule applies; an omitted list applies to every role."`
}

type Capabilities struct {
	OnMissing           MissingCapabilities `yaml:"on_missing" jsonschema_description:"Fallback for each missing capability kind; defaults to read_source for skills and lead_first for tools."`
	Hints               map[string]string   `yaml:"hints" jsonschema_description:"One-line guidance keyed by capability name; defaults to an empty map."`
	OnAuthFailure       string              `yaml:"on_auth_failure" jsonschema_description:"What happens when a worker cannot sign in to its agent or to an MCP server; defaults to login_then_lead, which runs the agent's login once and hands the task to the lead when that fails." jsonschema:"enum=login_then_lead,enum=lead,enum=fail"`
	LoginTimeoutSeconds int                 `yaml:"login_timeout_seconds" jsonschema_description:"Seconds to wait for a login the person must finish; defaults to 180."`
	MCPTools            map[string][]string `yaml:"mcp_tools" jsonschema_description:"Tools a bridged MCP server offers the worker, keyed by server name; a server not listed is bridged whole. Defaults to an empty map."`
}

type MissingCapabilities struct {
	Skill  string `yaml:"skill" jsonschema_description:"Handling for a missing skill; defaults to read_source to read its SKILL.md."`
	MCP    string `yaml:"mcp" jsonschema_description:"Handling for a missing MCP server; defaults to lead_first. bridge hands the lead's server, with its credentials, to the worker for one run." jsonschema:"enum=lead_first,enum=bridge,enum=reroute,enum=refuse"`
	Plugin string `yaml:"plugin" jsonschema_description:"Handling for a missing plugin; defaults to lead_first."`
}

type Sync struct {
	Auto   bool      `yaml:"auto" jsonschema_description:"Update generated files from hooks after choosing a direction; defaults to true."`
	Agents AgentSync `yaml:"agents" jsonschema_description:"Model, effort and frontmatter translation for agent files; defaults map vendor families."`
	Skills SkillSync `yaml:"skills" jsonschema_description:"Frontmatter translation for skill files; defaults preserve all keys."`
}

type AgentSync struct {
	ModelMap      map[string]string `yaml:"model_map" jsonschema_description:"Claude-to-Codex alias mapping; defaults map fable, opus, sonnet and haiku to astra, sol, terra and luna."`
	DefaultEffort map[string]string `yaml:"default_effort" jsonschema_description:"Effort per Claude alias when absent; defaults to high for fable and opus, medium for sonnet, low for haiku."`
	EffortMap     map[string]string `yaml:"effort_map" jsonschema_description:"Effort translation between agents; defaults preserve levels except ultra becomes max."`
	CodexName     string            `yaml:"codex_name" jsonschema_description:"Agent naming strategy, keep or underscore; defaults to keep."`
	DropKeys      []string          `yaml:"drop_keys" jsonschema_description:"Agent frontmatter keys explicitly discarded; defaults to color."`
	Overrides     map[string]Target `yaml:"overrides" jsonschema_description:"Per-agent model and effort overrides; defaults to an empty map."`
}

type SkillSync struct {
	DropKeys []string `yaml:"drop_keys" jsonschema_description:"Skill frontmatter keys explicitly discarded; defaults to an empty list."`
}

type AutoAnswer struct {
	Match  string `yaml:"match" jsonschema_description:"Text in a peer question to match, in list order."`
	Answer string `yaml:"answer" jsonschema_description:"Option label to select; trust and persistent approval grants are refused."`
}

// SafeAutoAnswer refuses labels that grant trust or bypass future approvals.
func SafeAutoAnswer(answer string) bool {
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer != "" && !strings.HasPrefix(answer, "trust") && !strings.HasPrefix(answer, "yes, and don't ask") && !strings.HasPrefix(answer, "always")
}

type Rex struct {
	StuckNotifyMinutes  int          `yaml:"stuck_notify_minutes" jsonschema_description:"Minutes an unanswered peer question waits before one notification from peer wait; defaults to 5. Zero disables reminders." jsonschema:"minimum=0"`
	AutoAnswers         []AutoAnswer `yaml:"auto_answers" jsonschema_description:"Ordered question and option rules; replaces defaults, and an empty list disables them. Defaults to continuing without trusting Codex hooks."`
	CaptureLines        int          `yaml:"capture_lines" jsonschema_description:"Terminal lines captured to inspect state; defaults to 20. Answers are read from files."`
	StartTimeoutSeconds int          `yaml:"start_timeout_seconds" jsonschema_description:"Seconds to wait for terminal startup; defaults to 120."`
}

// MCPToolRules turns capabilities.mcp_tools into gate allows: the tools a
// person listed for a server are the ones a worker may call without asking.
// They add to the policy rules, like learned rules, and a deny still wins.
func MCPToolRules(tools map[string][]string) []Rule {
	servers := make([]string, 0, len(tools))
	for server := range tools {
		servers = append(servers, server)
	}
	slices.Sort(servers)
	var rules []Rule
	for _, server := range servers {
		for _, tool := range tools[server] {
			if strings.TrimSpace(tool) != "" {
				rules = append(rules, Rule{Action: "mcp", Match: server + "/" + tool, Decision: "allow"})
			}
		}
	}
	return rules
}

// LearnedFile holds the allow rules a person approved during work with
// `duolog remember`. It is a separate file because a YAML layer replaces the
// whole rule list, and remembered commands must add to the project's rules.
const LearnedFile = "learned.yaml"
