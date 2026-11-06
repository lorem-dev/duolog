package schemagen

import (
	"encoding/json"
	"strconv"

	"github.com/invopop/jsonschema"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// Generate returns the configuration schema without reading configuration files.
func Generate() ([]byte, error) {
	reflector := jsonschema.Reflector{FieldNameTag: "yaml", RequiredFromJSONSchemaTags: true}
	schema := reflector.Reflect(&config.Config{})
	schema.ID = jsonschema.ID(config.SchemaURL)
	for _, name := range []string{"PaidAgent", "PaidClaude"} {
		requireCapWithAllow(schema.Definitions[name])
	}
	compactWindows(schema.Definitions["Workers"])
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
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
