package gateway

import (
	"testing"

	infraSchema "github.com/cordum/cordum/core/infra/schema"
)

func TestSchemaValidationModeDefaultsOffWhenUnset(t *testing.T) {
	s := &server{}

	if got := s.schemaValidationMode(); got != infraSchema.EnforcementOff {
		t.Fatalf("expected unset mode to default off, got %q", got)
	}
}

func TestSchemaValidationModeNormalizesConfiguredMode(t *testing.T) {
	s := &server{schemaEnforcement: infraSchema.EnforcementMode("ENFORCE")}

	if got := s.schemaValidationMode(); got != infraSchema.EnforcementEnforce {
		t.Fatalf("expected configured mode to normalize to enforce, got %q", got)
	}
}
