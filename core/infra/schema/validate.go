package schema

import (
	"bytes"
	"encoding/json"
	"fmt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
)

// ValidateSchema validates a value against a JSON schema payload.
func ValidateSchema(id string, schema []byte, value any) error {
	if len(schema) == 0 {
		return fmt.Errorf("schema is empty")
	}
	resourceID := schemaID(id)
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resourceID, bytes.NewReader(schema)); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := compiler.Compile(resourceID)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	payload, err := normalizeValue(value)
	if err != nil {
		return fmt.Errorf("normalize payload: %w", err)
	}
	if err := compiled.Validate(payload); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}

// ValidateMap validates a value against an inline schema map.
func ValidateMap(schema map[string]any, value any) error {
	if len(schema) == 0 {
		return fmt.Errorf("schema is empty")
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	return ValidateSchema("inline", data, value)
}

func normalizeValue(value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		var out any
		if err := json.Unmarshal(v, &out); err != nil {
			return nil, fmt.Errorf("decode payload: %w", err)
		}
		return out, nil
	case []byte:
		var out any
		if err := json.Unmarshal(v, &out); err != nil {
			return nil, fmt.Errorf("decode payload: %w", err)
		}
		return out, nil
	default:
		return value, nil
	}
}

func schemaID(id string) string {
	if id == "" {
		id = "schema"
	}
	return "inmemory://" + id
}
