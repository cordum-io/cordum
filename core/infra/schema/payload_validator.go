package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
)

// Violation captures a field-level JSON Schema validation failure.
type Violation struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidateJSONPayload validates a JSON payload against raw schema bytes, resolving
// cross-schema $ref links through the optional registry when present. If the
// payload is a persisted submit wrapper and contains a top-level `context`
// field, validation is applied to that logical context payload; otherwise the
// full payload is validated as-is.
func ValidateJSONPayload(ctx context.Context, registry *Registry, id string, schemaJSON, payloadJSON []byte) ([]Violation, error) {
	if len(bytes.TrimSpace(schemaJSON)) == 0 {
		return nil, fmt.Errorf("schema is empty")
	}

	validationPayload, err := extractValidationPayload(payloadJSON)
	if err != nil {
		return []Violation{{Path: "$", Message: "invalid JSON payload"}}, nil
	}

	compiler := jsonschema.NewCompiler()
	if registry != nil {
		compiler.LoadURL = func(url string) (io.ReadCloser, error) {
			data, err := registry.GetByURL(ctx, url)
			if err != nil {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		}
	}

	resourceID := schemaID(id)
	if err := compiler.AddResource(resourceID, bytes.NewReader(schemaJSON)); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := compiler.Compile(resourceID)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}

	var payload any
	if err := json.Unmarshal(validationPayload, &payload); err != nil {
		return []Violation{{Path: "$", Message: "invalid JSON payload"}}, nil
	}
	if err := compiled.Validate(payload); err != nil {
		var validationErr *jsonschema.ValidationError
		if errors.As(err, &validationErr) {
			return flattenValidationErrors(validationErr), nil
		}
		return nil, fmt.Errorf("validate payload: %w", err)
	}
	return nil, nil
}

func extractValidationPayload(payloadJSON []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(payloadJSON)
	if len(trimmed) == 0 {
		return []byte("null"), nil
	}

	var payload any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, fmt.Errorf("decode payload wrapper: %w", err)
	}

	root, ok := payload.(map[string]any)
	if !ok {
		return trimmed, nil
	}
	contextValue, hasContext := root["context"]
	if !hasContext {
		return trimmed, nil
	}
	data, err := json.Marshal(contextValue)
	if err != nil {
		return nil, fmt.Errorf("encode context payload: %w", err)
	}
	return data, nil
}

func flattenValidationErrors(err *jsonschema.ValidationError) []Violation {
	if err == nil {
		return nil
	}

	type key struct {
		path    string
		message string
	}

	seen := map[key]struct{}{}
	out := []Violation{}
	var walk func(current *jsonschema.ValidationError)
	walk = func(current *jsonschema.ValidationError) {
		if current == nil {
			return
		}
		if len(current.Causes) == 0 {
			item := Violation{
				Path:    normalizeValidationPath(current.InstanceLocation),
				Message: strings.TrimSpace(current.Message),
			}
			k := key{path: item.Path, message: item.Message}
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, item)
			}
			return
		}
		for _, cause := range current.Causes {
			walk(cause)
		}
	}
	walk(err)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Message < out[j].Message
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func normalizeValidationPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "$"
	}
	return path
}
