package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func vscodeGatewayRef() UpstreamServerRef {
	return UpstreamServerRef{
		Name:      "cordum",
		Transport: "http",
		Endpoint:  "https://gw.example:8081/api/v1/mcp",
		Tenant:    "tenant-a",
		AgentID:   "copilot-agent-1",
	}
}

func TestVSCodeAdapter_EmitsServersInputsHeaders(t *testing.T) {
	a := newVSCodeAdapter("/tmp/mcp.json")
	merged, _, err := a.ReadAndMerge(nil, vscodeGatewayRef())
	if err != nil {
		t.Fatalf("ReadAndMerge: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(merged, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	servers, _ := root["servers"].(map[string]any)
	entry, _ := servers["cordum"].(map[string]any)
	if entry["type"] != "http" || entry["url"] != "https://gw.example:8081/api/v1/mcp" {
		t.Fatalf("server entry = %+v, want http + url", entry)
	}
	headers, _ := entry["headers"].(map[string]any)
	if headers["X-API-Key"] != "${input:cordum-api-key}" {
		t.Fatalf("X-API-Key = %v, want ${input:cordum-api-key}", headers["X-API-Key"])
	}
	if headers["X-Tenant-ID"] != "tenant-a" || headers["X-Agent-Id"] != "copilot-agent-1" {
		t.Fatalf("tenant/agent headers = %+v", headers)
	}
	inputs, _ := root["inputs"].([]any)
	if len(inputs) != 1 {
		t.Fatalf("inputs = %+v, want 1", inputs)
	}
	in0, _ := inputs[0].(map[string]any)
	if in0["id"] != "cordum-api-key" || in0["password"] != true {
		t.Fatalf("input entry = %+v, want id=cordum-api-key password=true", in0)
	}
}

func TestVSCodeAdapter_NeverWritesLiteralSecret(t *testing.T) {
	// The adapter must never embed a literal key — only the ${input:...} ref.
	a := newVSCodeAdapter("/tmp/mcp.json")
	ref := vscodeGatewayRef()
	ref.AuthSecretRef = "secret://copilot/key" // even if a secret ref is passed
	merged, _, err := a.ReadAndMerge(nil, ref)
	if err != nil {
		t.Fatalf("ReadAndMerge: %v", err)
	}
	if strings.Contains(string(merged), "secret://copilot/key") {
		t.Fatalf("merged config leaked a secret ref:\n%s", merged)
	}
}

func TestVSCodeAdapter_PreservesExistingServersAndInputs(t *testing.T) {
	existing := []byte(`{
	  "servers": { "other": { "type": "http", "url": "https://other" } },
	  "inputs": [ { "id": "other-input", "type": "promptString" } ]
	}`)
	a := newVSCodeAdapter("/tmp/mcp.json")
	merged, _, err := a.ReadAndMerge(existing, vscodeGatewayRef())
	if err != nil {
		t.Fatalf("ReadAndMerge: %v", err)
	}
	var root map[string]any
	_ = json.Unmarshal(merged, &root)
	servers, _ := root["servers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatal("existing 'other' server was dropped")
	}
	if _, ok := servers["cordum"]; !ok {
		t.Fatal("cordum server not added")
	}
	inputs, _ := root["inputs"].([]any)
	if len(inputs) != 2 {
		t.Fatalf("inputs = %d, want 2 (preserve other-input + add cordum-api-key)", len(inputs))
	}
}

func TestVSCodeAdapter_IdempotentInputs(t *testing.T) {
	a := newVSCodeAdapter("/tmp/mcp.json")
	first, _, _ := a.ReadAndMerge(nil, vscodeGatewayRef())
	second, _, err := a.ReadAndMerge(first, vscodeGatewayRef())
	if err != nil {
		t.Fatalf("ReadAndMerge #2: %v", err)
	}
	var root map[string]any
	_ = json.Unmarshal(second, &root)
	inputs, _ := root["inputs"].([]any)
	if len(inputs) != 1 {
		t.Fatalf("re-apply produced %d inputs, want 1 (no duplicate cordum-api-key)", len(inputs))
	}
}
