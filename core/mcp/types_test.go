package mcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTypeJSONRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value any
		dst   func() any
	}{
		{
			name: "jsonrpc_request",
			value: JSONRPCRequest{
				JSONRPC: JSONRPCVersion,
				ID:      json.RawMessage(`1`),
				Method:  MethodInitialize,
				Params:  json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
			},
			dst: func() any { return &JSONRPCRequest{} },
		},
		{
			name: "jsonrpc_response",
			value: JSONRPCResponse{
				JSONRPC: JSONRPCVersion,
				ID:      json.RawMessage(`"abc"`),
				Result:  map[string]any{"ok": true},
			},
			dst: func() any { return &JSONRPCResponse{} },
		},
		{
			name: "initialize_result",
			value: InitializeResult{
				ProtocolVersion: DefaultProtocolVersion,
				Capabilities: ServerCapabilities{
					Tools:     &ToolsCapability{ListChanged: true},
					Resources: &ResourcesCapability{ListChanged: true},
				},
				ServerInfo: Implementation{Name: "cordum", Version: "1.0.0"},
			},
			dst: func() any { return &InitializeResult{} },
		},
		{
			name: "tool",
			value: Tool{
				Name:        "jobs.submit",
				Description: "submit a job",
				InputSchema: map[string]any{"type": "object"},
			},
			dst: func() any { return &Tool{} },
		},
		{
			name: "tool_call_result",
			value: ToolCallResult{
				Content: []ContentItem{
					{Type: "text", Text: "ok"},
				},
				StructuredContent: map[string]any{"id": "job-1"},
			},
			dst: func() any { return &ToolCallResult{} },
		},
		{
			name: "resource_template",
			value: ResourceTemplate{
				URITemplate: "cordum://jobs/{id}",
				Name:        "job",
				Description: "job detail",
			},
			dst: func() any { return &ResourceTemplate{} },
		},
		{
			name: "resource_read_result",
			value: ResourceReadResult{
				Contents: []ResourceContents{
					{URI: "cordum://jobs/1", MIMEType: "application/json", Text: `{"id":"1"}`},
				},
			},
			dst: func() any { return &ResourceReadResult{} },
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			dst := tc.dst()
			if err := json.Unmarshal(raw, dst); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			gotRaw, err := json.Marshal(dst)
			if err != nil {
				t.Fatalf("re-marshal failed: %v", err)
			}
			var wantAny any
			var gotAny any
			if err := json.Unmarshal(raw, &wantAny); err != nil {
				t.Fatalf("decode want json failed: %v", err)
			}
			if err := json.Unmarshal(gotRaw, &gotAny); err != nil {
				t.Fatalf("decode got json failed: %v", err)
			}
			if !reflect.DeepEqual(gotAny, wantAny) {
				t.Fatalf("round-trip mismatch\nwant=%v\ngot=%v", wantAny, gotAny)
			}
		})
	}
}
