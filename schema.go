package duolog

import _ "embed"

// ConfigSchema is the checked-in configuration schema, including its final newline.
//
//go:embed duolog-config.schema.json
var ConfigSchema []byte
