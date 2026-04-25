// Package llmchat provides the LLM provider abstraction behind the
// cordum-llm-chat service. Consumers call ResolveProvider with a
// ProviderConfig and receive a Provider whose Complete streams chunks
// back on a channel. The SamplingMode parameter is load-bearing:
// SamplingModeToolCalls uses the deterministic tool-call knobs, while
// SamplingModeSummary uses the higher-temperature prose knobs — the
// same Provider handles both phases so the caller does not juggle two
// clients.
//
// Only the OpenAI-compat provider ships in phase 1; future provider
// kinds (llamacpp, anthropic) join the switch in ResolveProvider
// without changing the Provider interface.
package llmchat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// SamplingMode selects which temperature + top_p pair the provider
// applies to a Complete call. Tool-call and summary phases of the
// agent loop have different UX requirements, so the provider exposes
// both via the same interface.
type SamplingMode int

const (
	// SamplingModeToolCalls biases the model toward deterministic
	// tool argument selection. Used during the tool-use iteration
	// phase of the agent loop.
	SamplingModeToolCalls SamplingMode = iota

	// SamplingModeSummary biases the model toward natural-language
	// prose. Used during the final-answer wrap-up phase.
	SamplingModeSummary
)

// String returns the canonical wire-name for a sampling mode; exposed
// so structured logs and traces can carry a human-readable value.
func (m SamplingMode) String() string {
	switch m {
	case SamplingModeToolCalls:
		return "tool_calls"
	case SamplingModeSummary:
		return "summary"
	default:
		return fmt.Sprintf("unknown(%d)", int(m))
	}
}

// ProviderConfig is the fully-resolved, validated provider configuration
// passed into ResolveProvider. Callers read env vars in the process
// entry-point and materialise this struct exactly once.
type ProviderConfig struct {
	// Kind is the provider type ("openai"). Future backends join via
	// the ResolveProvider switch; no inline defaulting so misconfig is
	// loud rather than silent.
	Kind string

	// BaseURL is the OpenAI-compat root, e.g. http://qwen-inference:8000/v1.
	// The provider appends /chat/completions and /models itself.
	BaseURL string

	// Model is the model name the backend expects (e.g. "qwen3-coder" —
	// the vLLM --served-model-name, never the Hugging Face path).
	Model string

	// APIKey is optional; populated when the backend requires Authorization
	// (omitted for local vLLM without auth).
	APIKey string

	// ToolTemperature + ToolTopP are applied when SamplingMode is
	// SamplingModeToolCalls. Plan default: 0.3 / 0.9.
	ToolTemperature float64
	ToolTopP        float64

	// SummaryTemperature + SummaryTopP are applied when SamplingMode is
	// SamplingModeSummary. Plan default: 0.7 / 0.8.
	SummaryTemperature float64
	SummaryTopP        float64
}

// BudgetConfig carries per-turn safety bounds for the agent loop.
// Phase 1 of the epic does not yet enforce them (no loop exists), but
// the shape is defined here so follow-up tasks slot in without
// reshuffling env-parsing code.
type BudgetConfig struct {
	MaxToolCallsPerTurn int
	MaxWallClockPerTurn time.Duration
	MaxAssistantBytes   int
}

// Message is one entry in the chat transcript fed to the model.
// Mirrors the OpenAI role/content shape with optional tool-call fields.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is a single tool invocation requested by the model.
// Arguments is JSON-encoded so callers can forward it to the MCP
// layer without an intermediate map allocation.
type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Tool is one tool exposed to the model in the request payload.
// Schema mirrors the OpenAI tools API.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// CompleteRequest carries the model input. Tools is optional; when
// empty, the provider omits the tools/tool_choice fields so a strict
// backend does not reject the request.
type CompleteRequest struct {
	Messages []Message
	Tools    []Tool
}

// Chunk is one frame of streaming output. Exactly one of Delta,
// ToolCalls, or Err is non-zero per chunk; Done=true terminates the
// stream (the channel is closed after the Done chunk).
type Chunk struct {
	// Delta is a partial text fragment from the assistant response.
	Delta string

	// ToolCalls holds tool-call deltas emitted by the model. Each
	// OpenAI-compat SSE frame may carry partial-args payloads; the
	// provider emits them as-seen and leaves assembly to the caller.
	ToolCalls []ToolCall

	// FinishReason is set on the terminal Chunk when the backend
	// supplies one ("stop", "tool_calls", "length", ...). Empty
	// otherwise.
	FinishReason string

	// Done is true on the final Chunk emitted before the channel
	// closes. Callers may treat Done as the loop exit signal.
	Done bool

	// Err is set when the stream aborts abnormally (network, 4xx,
	// retry exhaustion). A chunk with Err set is always the last
	// chunk before the channel closes.
	Err error
}

// Provider is the minimal surface a chat-capable LLM backend must
// implement. Keeping Complete + HealthCheck as the only two methods
// makes the interface trivially mockable (per task rail #1) and keeps
// provider-specific knobs out of the abstraction layer.
type Provider interface {
	// Complete begins a streaming chat-completion call. The caller
	// ranges over the returned channel; the channel is closed after
	// the final Chunk. An error returned from Complete itself
	// represents a pre-dispatch failure (invalid config); transient
	// stream errors are surfaced via Chunk.Err.
	Complete(ctx context.Context, req CompleteRequest, mode SamplingMode) (<-chan Chunk, error)

	// HealthCheck is used by /readyz to probe the backend without
	// performing a full completion. Implementations should use a
	// cheap endpoint (OpenAI-compat: GET /models) with a short
	// timeout.
	HealthCheck(ctx context.Context) error
}

// ResolveProvider returns a Provider matching the cfg.Kind. Unknown
// kinds fail closed — operators should see a crisp error rather than
// a silent fallback to a different backend.
func ResolveProvider(cfg ProviderConfig) (Provider, error) {
	switch cfg.Kind {
	case "openai":
		return NewOpenAIProvider(cfg), nil
	case "":
		return nil, fmt.Errorf("llmchat: provider kind is required")
	default:
		return nil, fmt.Errorf("llmchat: unknown provider kind %q", cfg.Kind)
	}
}
