package gateway

import (
	"context"
	"strings"
	"testing"
)

func TestValidateValidPayload(t *testing.T) {
	s, _, _ := newTestGateway(t)
	validator := newSchemaValidator(s.schemaRegistry)
	schemaJSON := []byte(`{
		"type": "object",
		"properties": {
			"message": {"type": "string"}
		},
		"required": ["message"]
	}`)
	payloadJSON := []byte(`{"context":{"message":"hello"}}`)

	violations, err := validator.Validate(context.Background(), "test/input", schemaJSON, payloadJSON)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %+v", violations)
	}
}

func TestValidateInvalidPayload(t *testing.T) {
	s, _, _ := newTestGateway(t)
	validator := newSchemaValidator(s.schemaRegistry)
	schemaJSON := []byte(`{
		"type": "object",
		"properties": {
			"message": {"type": "string"}
		},
		"required": ["message"]
	}`)
	payloadJSON := []byte(`{"context":{"message":123}}`)

	violations, err := validator.Validate(context.Background(), "test/input", schemaJSON, payloadJSON)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if len(violations) == 0 {
		t.Fatal("expected schema violations")
	}
	if violations[0].Path != "/message" {
		t.Fatalf("expected violation path /message, got %q", violations[0].Path)
	}
	if strings.TrimSpace(violations[0].Message) == "" {
		t.Fatalf("expected non-empty violation message, got %+v", violations[0])
	}
}

func TestValidateMalformedSchema(t *testing.T) {
	s, _, _ := newTestGateway(t)
	validator := newSchemaValidator(s.schemaRegistry)

	_, err := validator.Validate(context.Background(), "test/input", []byte(`{"type":123}`), []byte(`{"context":{"message":"hello"}}`))
	if err == nil {
		t.Fatal("expected malformed schema to return an error")
	}
}
