// Package contract publishes the language-independent v1 API contract.
package contract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
)

//go:embed schema.json
var Schema []byte

// Validate implements precisely the JSON Schema keywords used in schema.json.
// Keep this validator and the schema in sync; unsupported keywords are errors.
func Validate(value any) error {
	var root map[string]any
	if err := json.Unmarshal(Schema, &root); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var normalized any
	if err = json.NewDecoder(bytes.NewReader(raw)).Decode(&normalized); err != nil {
		return err
	}
	return validate(root, root, normalized, "$")
}
func validate(root, s map[string]any, v any, path string) error {
	for k := range s {
		switch k {
		case "$schema", "$id", "title", "description", "$defs", "$ref", "oneOf", "type", "properties", "required", "additionalProperties", "items", "enum", "const", "minimum", "minLength", "maxLength":
		default:
			return fmt.Errorf("unsupported schema keyword %s", k)
		}
	}
	if name, ok := s["$ref"].(string); ok {
		def := strings.TrimPrefix(name, "#/$defs/")
		target, ok := root["$defs"].(map[string]any)[def].(map[string]any)
		if !ok {
			return fmt.Errorf("unknown ref %s", name)
		}
		return validate(root, target, v, path)
	}
	if options, ok := s["oneOf"].([]any); ok {
		matches := 0
		for _, option := range options {
			if validate(root, option.(map[string]any), v, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: oneOf matched %d variants", path, matches)
		}
	}
	if want, ok := s["const"]; ok && !reflect.DeepEqual(want, v) {
		return fmt.Errorf("%s: const mismatch", path)
	}
	if values, ok := s["enum"].([]any); ok {
		found := false
		for _, item := range values {
			if reflect.DeepEqual(item, v) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: enum mismatch", path)
		}
	}
	if typ, ok := s["type"].(string); ok {
		valid := false
		switch typ {
		case "object":
			_, valid = v.(map[string]any)
		case "array":
			_, valid = v.([]any)
		case "string":
			_, valid = v.(string)
		case "boolean":
			_, valid = v.(bool)
		case "integer":
			n, ok := v.(float64)
			valid = ok && math.Trunc(n) == n
		default:
			return fmt.Errorf("unsupported type %s", typ)
		}
		if !valid {
			return fmt.Errorf("%s: expected %s", path, typ)
		}
	}
	if m, ok := v.(map[string]any); ok {
		if required, ok := s["required"].([]any); ok {
			for _, k := range required {
				if _, exists := m[k.(string)]; !exists {
					return fmt.Errorf("%s: missing %s", path, k)
				}
			}
		}
		props, _ := s["properties"].(map[string]any)
		for k, x := range m {
			if p, ok := props[k].(map[string]any); ok {
				if err := validate(root, p, x, path+"."+k); err != nil {
					return err
				}
			} else if allowed, ok := s["additionalProperties"].(bool); ok && !allowed {
				return fmt.Errorf("%s: unknown field %s", path, k)
			} else if p, ok := s["additionalProperties"].(map[string]any); ok {
				if err := validate(root, p, x, path+"."+k); err != nil {
					return err
				}
			}
		}
	}
	if a, ok := v.([]any); ok {
		if p, ok := s["items"].(map[string]any); ok {
			for _, x := range a {
				if err := validate(root, p, x, path+"[]"); err != nil {
					return err
				}
			}
		}
	}
	if text, ok := v.(string); ok {
		n := float64(len([]rune(text)))
		if min, ok := s["minLength"].(float64); ok && n < min {
			return fmt.Errorf("%s too short", path)
		}
		if max, ok := s["maxLength"].(float64); ok && n > max {
			return fmt.Errorf("%s too long", path)
		}
	}
	if n, ok := v.(float64); ok {
		if min, ok := s["minimum"].(float64); ok && n < min {
			return fmt.Errorf("%s too small", path)
		}
	}
	return nil
}
