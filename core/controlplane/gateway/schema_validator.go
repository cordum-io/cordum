package gateway

import (
	"context"

	infraSchema "github.com/cordum/cordum/core/infra/schema"
)

type schemaValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type schemaValidator struct {
	registry *infraSchema.Registry
}

func newSchemaValidator(registry *infraSchema.Registry) *schemaValidator {
	return &schemaValidator{registry: registry}
}

func (s *server) schemaValidationMode() infraSchema.EnforcementMode {
	if s == nil {
		return infraSchema.EnforcementWarn
	}
	return s.schemaEnforcement.Normalized()
}

func (v *schemaValidator) Validate(ctx context.Context, schemaID string, schemaJSON, payloadJSON []byte) ([]schemaValidationError, error) {
	violations, err := infraSchema.ValidateJSONPayload(ctx, registryForValidator(v), schemaID, schemaJSON, payloadJSON)
	if err != nil {
		return nil, err
	}
	if len(violations) == 0 {
		return nil, nil
	}
	out := make([]schemaValidationError, 0, len(violations))
	for _, violation := range violations {
		out = append(out, schemaValidationError{
			Path:    violation.Path,
			Message: violation.Message,
		})
	}
	return out, nil
}

func registryForValidator(v *schemaValidator) *infraSchema.Registry {
	if v == nil {
		return nil
	}
	return v.registry
}
