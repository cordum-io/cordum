package config

import (
	"fmt"
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
	schemaID := strings.ReplaceAll(name, " ", "-")
	if err := configschema.ValidateSchema(schemaID, schemaBytes, payload); err != nil {
		return fmt.Errorf("validate %s config: %w", name, err)
	}
	return nil
}
