package kit

import "encoding/json"

// AddFields sets the string fields in the JSON object out, a hook's stdout,
// and returns it with a newline. With no output it is an object of the fields
// alone. Bytes that are not a JSON object come back unchanged, so a field
// never breaks a hook's output. Other keys keep their values.
func AddFields(out []byte, fields map[string]string) []byte {
	object := map[string]json.RawMessage{}
	if len(out) > 0 {
		if json.Unmarshal(out, &object) != nil || object == nil {
			return out
		}
	}
	for k, v := range fields {
		encoded, err := json.Marshal(v)
		if err != nil {
			return out
		}
		object[k] = encoded
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return out
	}
	return append(encoded, '\n')
}
