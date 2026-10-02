package lineage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// UnmarshalJSON accepts flat qualified table keys and SQLGlot's nested
// catalog/schema/table objects. Column order is retained at every table leaf.
// A node cannot mix column types and nested objects; duplicate keys are errors.
func (s *Schema) UnmarshalJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	result := Schema{}
	var object func([]string) error
	object = func(path []string) error {
		if len(path) > 8 {
			return fmt.Errorf("schema nesting exceeds 8")
		}
		tok, err := d.Token()
		if err != nil {
			return err
		}
		if tok != json.Delim('{') {
			return fmt.Errorf("schema must contain objects")
		}
		fields := []Field{}
		seen := map[string]bool{}
		nested := false
		for d.More() {
			tok, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := tok.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid schema key %q", name)
			}
			seen[name] = true
			var raw json.RawMessage
			if err := d.Decode(&raw); err != nil {
				return err
			}
			if len(raw) > 0 && raw[0] == '{' {
				nested = true
				// A separate decoder keeps recursion independent of parent iteration.
				parent := d
				d = json.NewDecoder(bytes.NewReader(raw))
				err := object(append(append([]string{}, path...), name))
				d = parent
				if err != nil {
					return err
				}
			} else {
				var typ string
				if err := json.Unmarshal(raw, &typ); err != nil || typ == "" {
					return fmt.Errorf("schema type for %q must be a nonempty string", name)
				}
				fields = append(fields, Field{Name: name, Type: typ})
			}
		}
		if _, err := d.Token(); err != nil {
			return err
		}
		if len(fields) > 0 && (nested || len(path) == 0) {
			return fmt.Errorf("schema mixes column types and table objects")
		}
		if !nested && len(path) > 0 {
			key := strings.Join(path, ".")
			if _, exists := result[key]; exists {
				return fmt.Errorf("duplicate schema table %q", key)
			}
			result[key] = fields
		}
		return nil
	}
	if err := object(nil); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing schema data")
	}
	*s = result
	return nil
}
