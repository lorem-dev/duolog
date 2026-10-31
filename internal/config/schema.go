package config

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

const SchemaURL = "https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json"

// Schema returns the configuration schema without reading configuration files.
func Schema() ([]byte, error) {
	reflector := jsonschema.Reflector{FieldNameTag: "yaml", RequiredFromJSONSchemaTags: true}
	schema := reflector.Reflect(&Config{})
	schema.ID = jsonschema.ID(SchemaURL)
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
