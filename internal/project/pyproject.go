package project

import "github.com/pelletier/go-toml/v2"

// PyprojectTool returns the [tool] table of a project's pyproject.toml
// document as plain values: tables as maps, arrays as slices. A document
// without the table gives nil; a document that is not TOML gives the
// reader's error with what it read before it.
func PyprojectTool(doc []byte) (any, error) {
	var v struct {
		Tool any `toml:"tool"`
	}
	err := toml.Unmarshal(doc, &v)
	return v.Tool, err
}
