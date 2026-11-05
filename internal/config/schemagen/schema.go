package schemagen

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
	"github.com/lorem-dev/duolog/internal/config"
)

// Generate returns the configuration schema without reading configuration files.
func Generate() ([]byte, error) {
	reflector := jsonschema.Reflector{FieldNameTag: "yaml", RequiredFromJSONSchemaTags: true}
	schema := reflector.Reflect(&config.Config{})
	schema.ID = jsonschema.ID(config.SchemaURL)
	for _, name := range []string{"PaidAgent", "PaidClaude"} {
		requireCapWithAllow(schema.Definitions[name])
	}
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
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
