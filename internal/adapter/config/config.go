package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/store"
)

// restrictLayer applies the trust rules to layer i of the user, project and
// local files before it is merged onto base, and returns the notices and the
// name of the local layer when it is shared, "" when it is the person's own.
// The project layer is committed: it may only tighten paid runs, prices and
// the account session limits. So may a local layer that Git tracks, since it
// reaches everyone who clones the repository, and one outside a Git checkout,
// which nothing proves the person's. Every layer but the user config is also
// capped at the default total and tiers, whether Git ignores it or not.
func restrictLayer(i int, root string, base, node *yaml.Node) (notices []config.ConfigNotice, shared string, err error) {
	if i == 0 {
		return nil, "", nil
	}
	file := projectFile
	if i == 2 {
		file = localFile
		shared = sharedLocal(root)
	} else {
		shared = projectFile
	}
	if node == nil {
		return nil, shared, nil
	}
	if shared != "" {
		for _, restrict := range projectRestrictors {
			found, err := restrict(base, node, shared)
			if err != nil {
				return nil, "", err
			}
			notices = append(notices, found...)
		}
		if shared == outsideLocal && len(notices) > 0 {
			notices = append(notices, outsideNotice)
		}
	}
	found, err := capLayerSessionLimits(node, file)
	return append(notices, found...), shared, err
}

// Load merges embedded, user, project and local settings in that order.
func Load(root string) (*config.Config, error) {
	userDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("user config directory: %w", err)
	}
	var merged yaml.Node
	if err := mergeLayer(&merged, config.DefaultYAML(), "embedded config.default.yaml"); err != nil {
		return nil, err
	}
	paths := []string{
		filepath.Join(userDir, "duolog", "config.yaml"),
		filepath.Join(root, ".duolog", "config.yaml"),
		filepath.Join(root, ".duolog", "config.local.yaml"),
	}
	var userTelemetrySection config.Telemetry
	var view userView
	projectTelemetryOff := false
	layers := make([][]byte, len(paths))
	var notices []config.ConfigNotice
	localLabel := ""
	for i, path := range paths {
		if i == 1 {
			// Telemetry is user level: keep the section as it stands before
			// any project layer, which may only turn its own events off.
			if userTelemetrySection, err = userTelemetry(&merged); err != nil {
				return nil, err
			}
			// worker_modes binds to the roles and models the person defined.
			if view, err = decodeUserView(&merged); err != nil {
				return nil, err
			}
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fileError(path, err)
		}
		node, err := parseLayer(data, path)
		if err != nil {
			return nil, err
		}
		found, shared, err := restrictLayer(i, root, merged.Content[0], node)
		if err != nil {
			return nil, fileError(path, err)
		}
		notices = append(notices, found...)
		if i == 2 {
			localLabel = shared
		}
		mergeNode(&merged, node)
		if i > 0 {
			off, err := telemetryDisabled(data, path)
			if err != nil {
				return nil, err
			}
			projectTelemetryOff = projectTelemetryOff || off
		}
		layers[i] = data
	}
	var cfg config.Config
	if err := merged.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("merged config: %w", err)
	}
	if err := validateAccountLimits(&cfg); err != nil {
		return nil, err
	}
	cfg.Notices = notices
	cfg.Telemetry = keepTelemetry(userTelemetrySection, projectTelemetryOff, telemetryEnvOff())
	if err := validateTelemetry(cfg.Telemetry); err != nil {
		return nil, err
	}
	if cfg.Rex.StuckNotifyMinutes < 0 {
		return nil, fmt.Errorf("rex.stuck_notify_minutes must be zero or greater")
	}
	for name, role := range cfg.Roles {
		for _, target := range []config.Target{role.Use, role.Fallback} {
			if _, ok := cfg.Models[target.Model]; !ok && !strings.HasPrefix(target.Model, "gpt-") && !strings.HasPrefix(target.Model, "claude-") {
				return nil, fmt.Errorf("role %s: unknown model %q", name, target.Model)
			}
		}
	}
	for i, rule := range cfg.Rex.AutoAnswers {
		if strings.TrimSpace(rule.Match) == "" || !config.SafeAutoAnswer(rule.Answer) {
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
	cfg.Policy.Rules = append(cfg.Policy.Rules, config.MCPToolRules(cfg.Capabilities.MCPTools)...)
	if err := validateLearning(&cfg); err != nil {
		return nil, err
	}
	if err := validateCalibration(&cfg); err != nil {
		return nil, err
	}
	if err := validatePaidRuns(&cfg); err != nil {
		return nil, err
	}
	if err := lowerSharedCaps(&cfg, layers[0], layers[1], layers[2], localLabel); err != nil {
		return nil, err
	}
	if err := resolveWorkerModes(&cfg, view, layers[0], layers[1], layers[2]); err != nil {
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

type learnedRules struct {
	Rules []config.Rule `yaml:"rules"`
}

// LearnedRules reads .duolog/learned.yaml. Only allow rules count: a remembered
// command can widen what workers may run, never narrow or replace a deny, and
// the gate checks every deny before any allow.
func LearnedRules(root string) ([]config.Rule, error) {
	path := filepath.Join(root, ".duolog", config.LearnedFile)
	// duolog remember replaces the file with a rename, which a reader on
	// Windows can meet halfway.
	data, err := store.ReadFile(path)
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
	var rules []config.Rule
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
func keepDefaultDenies(cfg *config.Config) error {
	var defaults config.Config
	if err := yaml.Unmarshal(config.DefaultYAML(), &defaults); err != nil {
		return fmt.Errorf("embedded config.default.yaml: %w", err)
	}
	for _, rule := range defaults.Policy.Rules {
		if rule.Decision == "deny" && !slices.ContainsFunc(cfg.Policy.Rules, func(have config.Rule) bool {
			return have.Decision == rule.Decision && have.Action == rule.Action && have.Match == rule.Match
		}) {
			cfg.Policy.Rules = append(cfg.Policy.Rules, rule)
		}
	}
	return nil
}

func mergeLayer(dst *yaml.Node, data []byte, source string) error {
	node, err := parseLayer(data, source)
	if err != nil {
		return err
	}
	mergeNode(dst, node)
	return nil
}

// parseLayer checks one layer and returns its root with aliases and merge
// keys resolved, or nil for an empty file. Keys are checked on the node
// first so that an unknown key is named by its full path.
func parseLayer(data []byte, source string) (*yaml.Node, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fileError(source, err)
	}
	if len(node.Content) == 0 {
		return nil, nil
	}
	// Resolve structure without decoding scalars into Go values: string fields
	// must retain spellings such as 012 and 2020-01-01.
	effective, err := resolveYAMLNode(node.Content[0], make(map[*yaml.Node]bool))
	if err != nil {
		return nil, fileError(source, err)
	}
	if err := validateEnums(effective, reflect.TypeFor[config.Config](), "config"); err != nil {
		return nil, fileError(source, err)
	}
	if err := checkAccountLimitNodes(effective); err != nil {
		return nil, fileError(source, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var layer config.Config
	if err := decoder.Decode(&layer); err != nil {
		return nil, fileError(source, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("expected a single YAML document")
		}
		return nil, fileError(source, err)
	}
	return effective, nil
}

// mergeNode merges a resolved layer root into dst; nil leaves dst as it is.
func mergeNode(dst, root *yaml.Node) {
	if root == nil {
		return
	}
	if len(dst.Content) == 0 {
		*dst = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	} else {
		mergeMapping(dst.Content[0], root)
	}
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

// yamlField finds the struct field for a YAML key, including the fields of
// an inlined struct.
func yamlField(typ reflect.Type, key string) (reflect.StructField, bool) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name, opts, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" && opts == "inline" && field.Type.Kind() == reflect.Struct {
			if inner, ok := yamlField(field.Type, key); ok {
				return inner, true
			}
		} else if name == key && name != "-" {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

// Reject nulls and unknown keys and read enum tags from the types reflected into
// the public schema. Walk only explicitly supplied fields so partial override
// layers are valid.
func validateEnums(node *yaml.Node, typ reflect.Type, path string) error {
	if node.Kind == yaml.AliasNode {
		return validateEnums(node.Alias, typ, path)
	}
	if node.Tag == "!!null" {
		return atLine(node.Line, fmt.Errorf("%s: null is not allowed", path))
	}
	switch typ.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			break
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			field, ok := yamlField(typ, key)
			if !ok {
				return atLine(node.Content[i].Line, fmt.Errorf("%s.%s: unknown key", path, key))
			}
			var choices []string
			for _, tag := range strings.Split(field.Tag.Get("jsonschema"), ",") {
				if v, ok := strings.CutPrefix(tag, "enum="); ok {
					choices = append(choices, v)
				}
			}
			// worker_modes names are checked by resolveWorkerModes, which
			// says why a name is refused; the schema still lists them.
			if len(choices) > 0 && typ != reflect.TypeFor[config.WorkerMode]() {
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
					return atLine(actual.Line, fmt.Errorf("%s.%s: invalid value %q (want %s)", path, key, actual.Value, strings.Join(choices, ", ")))
				}
			}
			if err := validateEnums(value, field.Type, path+"."+key); err != nil {
				return err
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
