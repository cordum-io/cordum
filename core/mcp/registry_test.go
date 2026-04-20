package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestToolRegistrationAndCall(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()

	called := false
	err := registry.Register(
		Tool{
			Name:        "jobs.submit",
			Description: "submit a job",
			InputSchema: map[string]any{
				"type":       "object",
				"required":   []any{"topic"},
				"properties": map[string]any{"topic": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			called = true
			var payload map[string]any
			if err := json.Unmarshal(params, &payload); err != nil {
				return nil, err
			}
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "ok"}},
				StructuredContent: map[string]any{
					"topic": payload["topic"],
				},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("register tool failed: %v", err)
	}

	tools := registry.List()
	if len(tools) != 1 || tools[0].Name != "jobs.submit" {
		t.Fatalf("unexpected tools list: %+v", tools)
	}

	result, err := registry.Call(context.Background(), "jobs.submit", json.RawMessage(`{"topic":"job.echo"}`))
	if err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	if !called {
		t.Fatal("expected tool handler to be invoked")
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

func TestToolDisabledByConfig(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(Tool{Name: "jobs.submit"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	}); err != nil {
		t.Fatalf("register tool failed: %v", err)
	}
	registry.SetConfig(map[string]any{
		"mcp": map[string]any{
			"tools": map[string]any{
				"jobs.submit": map[string]any{"enabled": false},
			},
		},
	})

	if got := registry.List(); len(got) != 0 {
		t.Fatalf("expected disabled tool to be omitted from list, got %+v", got)
	}
	_, err := registry.Call(context.Background(), "jobs.submit", json.RawMessage(`{}`))
	if !errors.Is(err, ErrToolDisabled) {
		t.Fatalf("expected ErrToolDisabled, got %v", err)
	}
}

func TestResourceRegistrationAndRead(t *testing.T) {
	t.Parallel()
	registry := NewResourceRegistry()
	err := registry.Register(Resource{
		URI:      "cordum://status",
		Name:     "status",
		MIMEType: "application/json",
	}, func(_ context.Context, uri string) (*ResourceContents, error) {
		return &ResourceContents{URI: uri, MIMEType: "application/json", Text: `{"ok":true}`}, nil
	})
	if err != nil {
		t.Fatalf("register resource failed: %v", err)
	}

	resources := registry.List()
	if len(resources) != 1 || resources[0].URI != "cordum://status" {
		t.Fatalf("unexpected resources list: %+v", resources)
	}

	content, err := registry.Read(context.Background(), "cordum://status")
	if err != nil {
		t.Fatalf("resource read failed: %v", err)
	}
	if content.URI != "cordum://status" {
		t.Fatalf("unexpected resource uri: %q", content.URI)
	}
}

func TestResourceDisabledByConfig(t *testing.T) {
	t.Parallel()
	registry := NewResourceRegistry()
	if err := registry.Register(Resource{
		URI:  "cordum://status",
		Name: "status",
	}, func(_ context.Context, uri string) (*ResourceContents, error) {
		return &ResourceContents{URI: uri}, nil
	}); err != nil {
		t.Fatalf("register resource failed: %v", err)
	}
	registry.SetConfig(map[string]any{
		"mcp": map[string]any{
			"resources": map[string]any{
				"status": map[string]any{"enabled": false},
			},
		},
	})

	if got := registry.List(); len(got) != 0 {
		t.Fatalf("expected disabled resource to be omitted from list, got %+v", got)
	}
	_, err := registry.Read(context.Background(), "cordum://status")
	if !errors.Is(err, ErrResourceDisabled) {
		t.Fatalf("expected ErrResourceDisabled, got %v", err)
	}
}

func TestToolCallInvalidParams(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.validate",
			Description: "tool with required params",
			InputSchema: map[string]any{
				"type":       "object",
				"required":   []any{"name"},
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Missing required field "name" — should fail validation.
	_, err := registry.Call(context.Background(), "test.validate", json.RawMessage(`{"other":"value"}`))
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("expected ErrInvalidParams, got: %v", err)
	}
}

func TestToolCallValidParams(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.valid",
			Description: "tool with schema",
			InputSchema: map[string]any{
				"type":       "object",
				"required":   []any{"name"},
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	result, err := registry.Call(context.Background(), "test.valid", json.RawMessage(`{"name":"alice"}`))
	if err != nil {
		t.Fatalf("expected valid params to pass: %v", err)
	}
	if result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestToolCallEmptyParams(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.empty",
			Description: "tool with no required fields",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Empty params {} should pass when no required fields.
	result, err := registry.Call(context.Background(), "test.empty", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected empty params to pass: %v", err)
	}
	if result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestToolCallNullParams(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.null",
			Description: "tool accepting null params",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	// nil/empty params should be treated as empty map.
	_, err := registry.Call(context.Background(), "test.null", nil)
	if err != nil {
		t.Fatalf("expected nil params to pass: %v", err)
	}
}

func TestToolCallInvalidJSON(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.badjson",
			Description: "tool for testing bad JSON",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Malformed JSON should return an error.
	_, err := registry.Call(context.Background(), "test.badjson", json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON params")
	}
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("expected ErrInvalidParams, got: %v", err)
	}
}

func TestURITemplateMatching(t *testing.T) {
	t.Parallel()
	registry := NewResourceRegistry()
	if err := registry.RegisterTemplate(ResourceTemplate{
		URITemplate: "cordum://jobs/{id}",
		Name:        "job",
		MIMEType:    "application/json",
	}, func(_ context.Context, uri string) (*ResourceContents, error) {
		return &ResourceContents{URI: uri, MIMEType: "application/json", Text: `{"id":"123"}`}, nil
	}); err != nil {
		t.Fatalf("register template failed: %v", err)
	}

	templates := registry.ListTemplates()
	if len(templates) != 1 || templates[0].URITemplate != "cordum://jobs/{id}" {
		t.Fatalf("unexpected templates list: %+v", templates)
	}

	content, err := registry.Read(context.Background(), "cordum://jobs/123")
	if err != nil {
		t.Fatalf("template read failed: %v", err)
	}
	if content.URI != "cordum://jobs/123" {
		t.Fatalf("unexpected content uri: %q", content.URI)
	}
}
