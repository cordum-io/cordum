package codellm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeCodePromptUsesStructuredContext(t *testing.T) {
	payloadBytes := []byte(`{"file_path":"main.go","code_snippet":"func main() {}","instruction":"Add logging"}`)
	var ctxPayload codeContext
	if err := json.Unmarshal(payloadBytes, &ctxPayload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	outBytes, outCtx, prompt := normalizeCodePrompt(payloadBytes, ctxPayload)
	if string(outBytes) != string(payloadBytes) {
		t.Fatalf("expected payload bytes unchanged")
	}
	if outCtx.FilePath != "main.go" || outCtx.Instruction != "Add logging" {
		t.Fatalf("unexpected ctx: %#v", outCtx)
	}
	if !strings.Contains(prompt, "file main.go") || !strings.Contains(prompt, "Add logging") || !strings.Contains(prompt, "func main") {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
}

func TestNormalizeCodePromptUsesGatewayPromptWhenUnstructured(t *testing.T) {
	payloadBytes := []byte(`{"prompt":"do the thing","topic":"job.code.llm"}`)
	outBytes, outCtx, prompt := normalizeCodePrompt(payloadBytes, codeContext{})
	if string(outBytes) != string(payloadBytes) {
		t.Fatalf("expected payload bytes unchanged")
	}
	if prompt != "do the thing" {
		t.Fatalf("expected prompt=%q got=%q", "do the thing", prompt)
	}
	if outCtx.Instruction != "do the thing" {
		t.Fatalf("expected instruction=%q got=%q", "do the thing", outCtx.Instruction)
	}
}

func TestNormalizeCodePromptUsesGatewayNestedContext(t *testing.T) {
	payloadBytes := []byte(`{"prompt":"ignored","context":{"file_path":"x.go","code_snippet":"package main","instruction":"Fix it"}}`)
	outBytes, outCtx, prompt := normalizeCodePrompt(payloadBytes, codeContext{})
	if strings.TrimSpace(outCtx.FilePath) != "x.go" || strings.TrimSpace(outCtx.Instruction) != "Fix it" {
		t.Fatalf("unexpected ctx: %#v", outCtx)
	}
	if !strings.Contains(prompt, "file x.go") || !strings.Contains(prompt, "Fix it") || !strings.Contains(prompt, "package main") {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
	var raw map[string]any
	if err := json.Unmarshal(outBytes, &raw); err != nil {
		t.Fatalf("expected nested payload bytes to be valid json: %v", err)
	}
	if raw["file_path"] != "x.go" {
		t.Fatalf("expected nested payload bytes, got: %#v", raw)
	}
}

