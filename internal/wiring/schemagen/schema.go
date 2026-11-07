package schemagen

import (
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/invopop/jsonschema"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// Generate returns the configuration schema without reading configuration
// files. It names the registered agents under each agent map.
func Generate() ([]byte, error) {
	return GenerateFor(wiring.Descriptors())
}

// GenerateFor is Generate for the given agents, in order.
func GenerateFor(agents []agent.Descriptor) ([]byte, error) {
	reflector := jsonschema.Reflector{FieldNameTag: "yaml", RequiredFromJSONSchemaTags: true}
	schema := reflector.Reflect(&config.Config{})
	schema.ID = jsonschema.ID(config.SchemaURL)
	// No field names PaidAgent any more, so reflect it for its definition.
	schema.Definitions["PaidAgent"] = reflector.ReflectFromType(reflect.TypeFor[config.PaidAgent]()).Definitions["PaidAgent"]
	paidAgents(schema, agents)
	coauthors(schema.Definitions["Coauthors"], agents)
	workerMode(schema.Definitions["WorkerMode"], agents)
	agentSwitches(schema, agents)
	agentEnum(schema.Definitions["Target"], agents)
	agentEnum(schema.Definitions["Model"], agents)
	compactWindows(schema.Definitions["Workers"])
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// without returns a copy of the properties that leaves out the named one.
func without(props *jsonschema.Schema, name string) *jsonschema.Schema {
	c := *props
	c.Properties = jsonschema.NewProperties()
	for pair := props.Properties.Oldest(); pair != nil; pair = pair.Next() {
		if pair.Key != name {
			c.Properties.Set(pair.Key, pair.Value)
		}
	}
	return &c
}

// paidAgents writes the definition of one agent's paid settings, with
// subscription_endpoints for the agents that have endpoints, and one property
// of paid_runs per agent.
func paidAgents(schema *jsonschema.Schema, agents []agent.Descriptor) {
	full := schema.Definitions["PaidAgent"]
	endpoints, _ := full.Properties.Get("subscription_endpoints")
	plain := without(full, "subscription_endpoints")
	requireCapWithAllow(plain)
	schema.Definitions["PaidAgent"] = plain
	paid := schema.Definitions["PaidRuns"]
	props := jsonschema.NewProperties()
	if share, ok := paid.Properties.Get("usable_share"); ok {
		props.Set("usable_share", share)
	}
	for _, a := range agents {
		ref := "PaidAgent"
		if a.Config.SubscriptionEndpoints {
			ref = "Paid" + a.Display
			clone := *plain
			with := &clone
			with.Properties = jsonschema.NewProperties()
			for pair := plain.Properties.Oldest(); pair != nil; pair = pair.Next() {
				with.Properties.Set(pair.Key, pair.Value)
			}
			with.Properties.Set("subscription_endpoints", endpoints)
			schema.Definitions[ref] = with
		}
		props.Set(a.Name, &jsonschema.Schema{
			Ref:         "#/$defs/" + ref,
			Description: "Paid runs of " + a.Display + " workers and peers; off by default.",
		})
	}
	paid.Properties = props
}

// coauthors writes one template property per agent after enabled.
func coauthors(s *jsonschema.Schema, agents []agent.Descriptor) {
	for _, a := range agents {
		s.Properties.Set(a.Name, &jsonschema.Schema{Type: "string", Description: a.Config.CoauthorDoc})
	}
}

// workerMode writes one native name property per agent before network and
// writable_roots.
func workerMode(s *jsonschema.Schema, agents []agent.Descriptor) {
	props := jsonschema.NewProperties()
	for _, a := range agents {
		var modes []any
		for _, m := range a.Config.NativeModes {
			modes = append(modes, m)
		}
		props.Set(a.Name, &jsonschema.Schema{Type: "string", Enum: modes, Description: a.Config.NativeModeDoc})
	}
	for pair := s.Properties.Oldest(); pair != nil; pair = pair.Next() {
		props.Set(pair.Key, pair.Value)
	}
	s.Properties = props
}

// agentSwitches writes one property of the agents section per agent. Its
// definition has the enabled key alone, and each agent with a skills_dir key
// has a definition of its own that adds it.
func agentSwitches(schema *jsonschema.Schema, agents []agent.Descriptor) {
	section, ok := schema.Definitions["Config"].Properties.Get("agents")
	if !ok {
		return
	}
	section.AdditionalProperties = jsonschema.FalseSchema
	section.Properties = jsonschema.NewProperties()
	full := schema.Definitions["AgentSwitch"]
	skills, _ := full.Properties.Get("skills_dir")
	plain := without(full, "skills_dir")
	schema.Definitions["AgentSwitch"] = plain
	for _, a := range agents {
		ref := "AgentSwitch"
		if a.Config.SkillsDir != "" {
			ref = "AgentSwitch" + a.Display
			clone := *plain
			with := &clone
			with.Properties = jsonschema.NewProperties()
			for pair := plain.Properties.Oldest(); pair != nil; pair = pair.Next() {
				with.Properties.Set(pair.Key, pair.Value)
			}
			doc := *skills
			doc.Description = "Folder of " + a.Display + "'s project skills, relative to the project root and written with slashes; set it in the project's .duolog/config.yaml or config.local.yaml, not the user config. User scope is not affected. Defaults to " + a.Config.SkillsDir + "."
			with.Properties.Set("skills_dir", &doc)
			schema.Definitions[ref] = with
		}
		section.Properties.Set(a.Name, &jsonschema.Schema{Ref: "#/$defs/" + ref, Description: "Switch for " + a.Display + "."})
	}
}

// agentEnum limits the agent property of a definition to the registered agents.
func agentEnum(s *jsonschema.Schema, agents []agent.Descriptor) {
	prop, ok := s.Properties.Get("agent")
	if !ok {
		return
	}
	prop.Enum = nil
	for _, a := range agents {
		prop.Enum = append(prop.Enum, a.Name)
	}
}

// compactWindows spells the values of workers.compact_window (auto, 0 or a
// number) and of each compact_ladder entry (0 or a number), as Load checks them.
func compactWindows(s *jsonschema.Schema) {
	number := func() []*jsonschema.Schema {
		return []*jsonschema.Schema{
			{Type: "integer", Const: 0},
			{Type: "integer", Minimum: json.Number(strconv.Itoa(config.MinCompactWindow)), Maximum: json.Number(strconv.Itoa(config.MaxCompactWindow))},
		}
	}
	if window, ok := s.Properties.Get("compact_window"); ok {
		window.Type = ""
		window.OneOf = append([]*jsonschema.Schema{{Type: "string", Enum: []any{"auto"}}}, number()...)
	}
	if ladder, ok := s.Properties.Get("compact_ladder"); ok {
		ladder.AdditionalProperties = &jsonschema.Schema{OneOf: number()}
	}
}

// requireCapWithAllow makes allow: true need a daily_cap_usd above 0 in the
// same mapping, as Load does for the merged layers.
func requireCapWithAllow(s *jsonschema.Schema) {
	allow := jsonschema.NewProperties()
	allow.Set("allow", &jsonschema.Schema{Const: true, Description: "Paid runs are allowed."})
	capAboveZero := jsonschema.NewProperties()
	capAboveZero.Set("daily_cap_usd", &jsonschema.Schema{ExclusiveMinimum: "0", Description: "Allowed paid runs need a daily cap above 0."})
	s.If = &jsonschema.Schema{Properties: allow, Required: []string{"allow"}}
	s.Then = &jsonschema.Schema{Properties: capAboveZero, Required: []string{"daily_cap_usd"}}
}
