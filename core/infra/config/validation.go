package config

import (
	"fmt"
	"math"
	"strings"

	configschema "github.com/cordum/cordum/core/infra/schema"
	"gopkg.in/yaml.v3"
)

func validateConfigSchema(name, schemaPath string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	schemaBytes, err := configSchemaFS.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("load %s schema: %w", name, err)
	}
	var payload any
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("parse %s config: %w", name, err)
	}
	if hasNonFiniteNumber(payload) {
		return fmt.Errorf("validate %s config: numeric values must be finite", name)
	}
	schemaID := strings.ReplaceAll(name, " ", "-")
	if err := configschema.ValidateSchema(schemaID, schemaBytes, payload); err != nil {
		return fmt.Errorf("validate %s config: %w", name, err)
	}
	return nil
}

func hasNonFiniteNumber(value any) bool {
	switch v := value.(type) {
	case float64:
		return math.IsNaN(v) || math.IsInf(v, 0)
	case map[string]any:
		for _, item := range v {
			if hasNonFiniteNumber(item) {
				return true
			}
		}
	case map[any]any:
		for key, item := range v {
			if hasNonFiniteNumber(key) || hasNonFiniteNumber(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if hasNonFiniteNumber(item) {
				return true
			}
		}
	}
	return false
}
