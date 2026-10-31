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
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
