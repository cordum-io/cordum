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
