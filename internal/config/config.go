package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	SkillTests     string           `yaml:"skill_tests" jsonschema_description:"Cross-model skill testing preference: ask before each change, always test, or never test; defaults to ask." jsonschema:"enum=ask,enum=always,enum=never"`
	Coauthors      Coauthors        `yaml:"coauthors" jsonschema_description:"Commit co-author templates for workers; enabled by default."`
	Version        int              `yaml:"version" jsonschema_description:"Configuration format version; defaults to 1."`
	Wire           Wire             `yaml:"wire" jsonschema_description:"Task and message encoding and size limits; defaults suit the file bus."`
	Limits         Limits           `yaml:"limits" jsonschema_description:"Worker delegation limits; defaults permit one worker per agent."`
	Models         map[string]Model `yaml:"models" jsonschema_description:"Model aliases and price metadata; defaults include the eight vendor families."`
	Roles          map[string]Role  `yaml:"roles" jsonschema_description:"Named task routes; overrides merge by role and field into the eleven defaults."`
	Sessions       Sessions         `yaml:"sessions" jsonschema_description:"Thread lifetime limits; defaults close large or idle threads."`
	Policy         Policy           `yaml:"policy" jsonschema_description:"Permission rules constrained by the lead; defaults ask for unmatched actions."`
	Capabilities   Capabilities     `yaml:"capabilities" jsonschema_description:"Missing capability handling; defaults read skills and let the lead handle tools."`
	Sync           Sync             `yaml:"sync" jsonschema_description:"Settings for generating agent and skill files; automatic sync defaults to true."`
	EnvPassthrough []string         `yaml:"env_passthrough" jsonschema_description:"Environment variables passed to workers; defaults include native OS login, API keys and agent config directories."`
	Rex            Rex              `yaml:"rex" jsonschema_description:"Terminal capture and startup settings; defaults to 20 lines and 120 seconds."`
	Learning       Learning         `yaml:"learning" jsonschema_description:"Learning mode: task classes, classification rules, ladders and priors; off by default."`
	Telemetry      Telemetry        `yaml:"telemetry" jsonschema_description:"Local telemetry that never leaves the machine; on by default. User level, except that a project may turn off its own events."`
}

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
	MaxLiveSessions  int      `yaml:"max_live_sessions" jsonschema_description:"Maximum live resumable sessions per agent; defaults to 1 and excludes fresh runs."`
	SelfMaxReadFiles int      `yaml:"self_max_read_files" jsonschema_description:"Maximum files for work handled by the lead on a provider limit; defaults to 3."`
	SelfMaxReadLines int      `yaml:"self_max_read_lines" jsonschema_description:"Maximum lines for work handled by the lead on a provider limit; defaults to 200."`
}

type Model struct {
	Agent     string  `yaml:"agent" jsonschema_description:"Vendor agent for this alias; defaults to claude or codex according to the family." jsonschema:"enum=claude,enum=codex"`
	Family    string  `yaml:"family" jsonschema_description:"Codex model family used to resolve the newest visible version; absent for Claude aliases."`
	PricedFor string  `yaml:"priced_for" jsonschema_description:"Version whose prices are recorded; used for estimates, never for routing."`
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

// Load merges embedded, user, project and local settings in that order.
func Load(root string) (*Config, error) {
	userDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("user config directory: %w", err)
	}
	var merged yaml.Node
	if err := mergeLayer(&merged, defaultConfig, "embedded config.default.yaml"); err != nil {
		return nil, err
	}
	paths := []string{
		filepath.Join(userDir, "duolog", "config.yaml"),
		filepath.Join(root, ".duolog", "config.yaml"),
		filepath.Join(root, ".duolog", "config.local.yaml"),
	}
	var userTelemetrySection Telemetry
	projectTelemetryOff := false
	for i, path := range paths {
		if i == 1 {
			// Telemetry is user level: keep the section as it stands before
			// any project layer, which may only turn its own events off.
			if userTelemetrySection, err = userTelemetry(&merged); err != nil {
				return nil, err
			}
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := mergeLayer(&merged, data, path); err != nil {
			return nil, err
		}
		if i > 0 {
			off, err := telemetryDisabled(data, path)
			if err != nil {
				return nil, err
			}
			projectTelemetryOff = projectTelemetryOff || off
		}
	}
	var cfg Config
	if err := merged.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("merged config: %w", err)
	}
	cfg.Telemetry = keepTelemetry(userTelemetrySection, projectTelemetryOff, telemetryEnvOff())
	if err := validateTelemetry(cfg.Telemetry); err != nil {
		return nil, err
	}
	if cfg.Rex.StuckNotifyMinutes < 0 {
		return nil, fmt.Errorf("rex.stuck_notify_minutes must be zero or greater")
	}
	for name, role := range cfg.Roles {
		for _, target := range []Target{role.Use, role.Fallback} {
			if _, ok := cfg.Models[target.Model]; !ok && !strings.HasPrefix(target.Model, "gpt-") && !strings.HasPrefix(target.Model, "claude-") {
				return nil, fmt.Errorf("role %s: unknown model %q", name, target.Model)
			}
		}
	}
	for i, rule := range cfg.Rex.AutoAnswers {
		if strings.TrimSpace(rule.Match) == "" || !SafeAutoAnswer(rule.Answer) {
			return nil, fmt.Errorf("rex.auto_answers[%d]: needs nonempty match and safe answer; cannot grant trust or skip approvals", i)
		}
	}
	platformDefaults(&cfg)
	if err := keepDefaultDenies(&cfg); err != nil {
		return nil, err
	}
	learned, err := LearnedRules(root)
	if err != nil {
		return nil, err
	}
	cfg.Policy.Rules = append(cfg.Policy.Rules, learned...)
	cfg.Policy.Rules = append(cfg.Policy.Rules, MCPToolRules(cfg.Capabilities.MCPTools)...)
	if err := validateLearning(&cfg); err != nil {
		return nil, err
	}
	if cfg.Learning.Enabled {
		routing, err := ReadLearnedRouting(root)
		if err != nil {
			return nil, err
		}
		cfg.Learning.Learned = routing.Ladders
	}
	return &cfg, nil
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

type learnedRules struct {
	Rules []Rule `yaml:"rules"`
}

// LearnedRules reads .duolog/learned.yaml. Only allow rules count: a remembered
// command can widen what workers may run, never narrow or replace a deny, and
// the gate checks every deny before any allow.
func LearnedRules(root string) ([]Rule, error) {
	path := filepath.Join(root, ".duolog", LearnedFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var file learnedRules
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var rules []Rule
	for _, rule := range file.Rules {
		if rule.Decision == "allow" && (rule.Action == "exec" || rule.Action == "mcp") && strings.TrimSpace(rule.Match) != "" {
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// keepDefaultDenies holds every default deny when a layer replaces the rule list.
// YAML replaces a list as a whole, and a project that adds one allow rule must not
// lose .git/** or git push by doing so.
func keepDefaultDenies(cfg *Config) error {
	var defaults Config
	if err := yaml.Unmarshal(defaultConfig, &defaults); err != nil {
		return fmt.Errorf("embedded config.default.yaml: %w", err)
	}
	for _, rule := range defaults.Policy.Rules {
		if rule.Decision == "deny" && !slices.ContainsFunc(cfg.Policy.Rules, func(have Rule) bool {
			return have.Decision == rule.Decision && have.Action == rule.Action && have.Match == rule.Match
		}) {
			cfg.Policy.Rules = append(cfg.Policy.Rules, rule)
		}
	}
	return nil
}

func mergeLayer(dst *yaml.Node, data []byte, source string) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var layer Config
	if err := decoder.Decode(&layer); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("expected a single YAML document")
		}
		return fmt.Errorf("%s: %w", source, err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if len(node.Content) == 0 {
		return nil
	}
	// Resolve structure without decoding scalars into Go values: string fields
	// must retain spellings such as 012 and 2020-01-01.
	effective, err := resolveYAMLNode(node.Content[0], make(map[*yaml.Node]bool))
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	node.Content[0] = effective
	if err := validateEnums(node.Content[0], reflect.TypeFor[Config](), "config"); err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if len(dst.Content) == 0 {
		*dst = node
	} else {
		mergeMapping(dst.Content[0], node.Content[0])
	}
	return nil
}

// Expand aliases into independent nodes and flatten YAML merge keys. Explicit
// keys win over merged keys, and earlier maps in a merge sequence win over later
// maps. Copies prevent layer merging from mutating another use of an anchor.
func resolveYAMLNode(node *yaml.Node, active map[*yaml.Node]bool) (*yaml.Node, error) {
	if active[node] {
		return nil, fmt.Errorf("line %d: recursive YAML alias", node.Line)
	}
	active[node] = true
	defer delete(active, node)
	if node.Kind == yaml.AliasNode {
		return resolveYAMLNode(node.Alias, active)
	}
	resolved := *node
	resolved.Anchor = ""
	resolved.Content = nil
	for _, child := range node.Content {
		value, err := resolveYAMLNode(child, active)
		if err != nil {
			return nil, err
		}
		resolved.Content = append(resolved.Content, value)
	}
	if resolved.Kind != yaml.MappingNode {
		return &resolved, nil
	}
	entries := resolved.Content
	resolved.Content = nil
	seen := make(map[string]bool)
	for i := 0; i < len(entries); i += 2 {
		key := entries[i]
		if key.Tag != "!!merge" {
			seen[key.Value] = true
			resolved.Content = append(resolved.Content, key, entries[i+1])
		}
	}
	for i := 0; i < len(entries); i += 2 {
		if entries[i].Tag != "!!merge" {
			continue
		}
		maps := []*yaml.Node{entries[i+1]}
		if maps[0].Kind == yaml.SequenceNode {
			maps = maps[0].Content
		}
		for _, mapping := range maps {
			if mapping.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("line %d: YAML merge requires a mapping", mapping.Line)
			}
			for j := 0; j < len(mapping.Content); j += 2 {
				key := mapping.Content[j]
				if !seen[key.Value] {
					seen[key.Value] = true
					resolved.Content = append(resolved.Content, key, mapping.Content[j+1])
				}
			}
		}
	}
	return &resolved, nil
}

// Mapping entries merge recursively; scalar and sequence values replace their layer.
func mergeMapping(dst, src *yaml.Node) {
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		*dst = *src
		return
	}
	for i := 0; i < len(src.Content); i += 2 {
		key, value := src.Content[i], src.Content[i+1]
		found := false
		for j := 0; j < len(dst.Content); j += 2 {
			if dst.Content[j].Value == key.Value {
				mergeMapping(dst.Content[j+1], value)
				found = true
				break
			}
		}
		if !found {
			dst.Content = append(dst.Content, key, value)
		}
	}
}

// Reject nulls and read enum tags from the types reflected into the public schema.
// Walk only explicitly supplied fields so partial override layers are valid.
func validateEnums(node *yaml.Node, typ reflect.Type, path string) error {
	if node.Kind == yaml.AliasNode {
		return validateEnums(node.Alias, typ, path)
	}
	if node.Tag == "!!null" {
		return fmt.Errorf("%s: null is not allowed", path)
	}
	switch typ.Kind() {
	case reflect.Struct:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			for j := 0; j < typ.NumField(); j++ {
				field := typ.Field(j)
				if field.Tag.Get("yaml") != key {
					continue
				}
				var choices []string
				for _, tag := range strings.Split(field.Tag.Get("jsonschema"), ",") {
					if v, ok := strings.CutPrefix(tag, "enum="); ok {
						choices = append(choices, v)
					}
				}
				if len(choices) > 0 {
					actual := value
					if actual.Kind == yaml.AliasNode {
						actual = actual.Alias
					}
					valid := false
					for _, choice := range choices {
						if actual.Tag == "!!str" && actual.Value == choice {
							valid = true
							break
						}
					}
					if !valid {
						return fmt.Errorf("%s.%s: invalid value %q (want %s)", path, key, actual.Value, strings.Join(choices, ", "))
					}
				}
				if err := validateEnums(value, field.Type, path+"."+key); err != nil {
					return err
				}
				break
			}
		}
	case reflect.Map:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := validateEnums(node.Content[i+1], typ.Elem(), path+"."+node.Content[i].Value); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i, child := range node.Content {
			if err := validateEnums(child, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
