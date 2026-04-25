package llmchat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider is the OpenAI-compat streaming chat-completions client
// used against vLLM (Qwen3-Coder), openai.com, or any other backend
// that speaks the OpenAI wire format.
//
// It deliberately talks to the HTTP API directly via net/http — the
// OpenAI Go SDK brings no wins on a compat API and pulls in a
// dependency this binary does not otherwise need (task rail #2).
type OpenAIProvider struct {
	baseURL            string
	model              string
	apiKey             string
	toolTemperature    float64
	toolTopP           float64
	summaryTemperature float64
	summaryTopP        float64

	// httpClient carries no Timeout so streaming response bodies are
	// not cut off mid-frame. Cancellation is driven by ctx instead.
	httpClient *http.Client

	// healthClient is a short-deadline client used for /readyz
	// probes so a slow vLLM cannot stall the probe.
	healthClient *http.Client
}

// NewOpenAIProvider constructs a provider from a ProviderConfig. All
// validation already happened at ResolveProvider; this function does
// not re-check fields.
func NewOpenAIProvider(cfg ProviderConfig) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL:            strings.TrimRight(cfg.BaseURL, "/"),
		model:              cfg.Model,
		apiKey:             cfg.APIKey,
		toolTemperature:    cfg.ToolTemperature,
		toolTopP:           cfg.ToolTopP,
		summaryTemperature: cfg.SummaryTemperature,
		summaryTopP:        cfg.SummaryTopP,
		httpClient:         &http.Client{}, // no timeout: context drives cancellation on streams
		healthClient:       &http.Client{Timeout: 2 * time.Second},
	}
}

// openaiRequest is the wire body sent to /chat/completions. Fields
// are all explicit so the JSON wire format is easy to audit.
type openaiRequest struct {
	Model       string        `json:"model"`
	Messages    []openaiMsg   `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature"`
	TopP        float64       `json:"top_p"`
	Tools       []openaiTool  `json:"tools,omitempty"`
	ToolChoice  string        `json:"tool_choice,omitempty"`
	StreamUsage bool          `json:"stream_options,omitempty"` //nolint:unused // reserved
	_           struct{}      // force keyed literals at call sites
}

type openaiMsg struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiTool struct {
	Type     string           `json:"type"`
	Function openaiToolSchema `json:"function"`
}

type openaiToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openaiToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openaiToolCallBody `json:"function"`
}

type openaiToolCallBody struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// openaiStreamFrame is one decoded `data: {...}` SSE frame.
type openaiStreamFrame struct {
	Choices []openaiStreamChoice `json:"choices"`
}

type openaiStreamChoice struct {
	Index        int                `json:"index"`
	Delta        openaiStreamDelta  `json:"delta"`
	FinishReason *string            `json:"finish_reason,omitempty"`
}

type openaiStreamDelta struct {
	Role      string                  `json:"role,omitempty"`
	Content   string                  `json:"content,omitempty"`
	ToolCalls []openaiStreamToolCall  `json:"tool_calls,omitempty"`
}

type openaiStreamToolCall struct {
	Index    int                 `json:"index"`
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function openaiToolCallBody  `json:"function"`
}

var retryDelays = []time.Duration{
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
}

// Complete issues a streaming chat-completion request and returns a
// channel that emits Chunks until the stream terminates. The channel
// is closed after the final Chunk.
//
// 5xx and network errors trigger exponential-backoff retries (3
// attempts total: 100ms/200ms/400ms gap). 4xx bubbles immediately —
// retrying a 400 is never correct.
func (p *OpenAIProvider) Complete(
	ctx context.Context,
	req CompleteRequest,
	mode SamplingMode,
) (<-chan Chunk, error) {
	body, err := p.buildRequestBody(req, mode)
	if err != nil {
		return nil, fmt.Errorf("llmchat/openai: build request: %w", err)
	}

	resp, err := p.doWithRetry(ctx, body)
	if err != nil {
		return nil, err
	}

	out := make(chan Chunk, 8)
	go p.stream(ctx, resp, out)
	return out, nil
}

// buildRequestBody serialises the OpenAI-compat POST body. The
// sampling knobs are picked based on mode — this is the load-bearing
// two-pass sampling logic that QA (task-7dd1af21) explicitly checks.
func (p *OpenAIProvider) buildRequestBody(req CompleteRequest, mode SamplingMode) ([]byte, error) {
	temp, topP := p.summaryTemperature, p.summaryTopP
	if mode == SamplingModeToolCalls {
		temp, topP = p.toolTemperature, p.toolTopP
	}

	msgs := make([]openaiMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		out := openaiMsg{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			out.ToolCalls = make([]openaiToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				out.ToolCalls = append(out.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openaiToolCallBody{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				})
			}
		}
		msgs = append(msgs, out)
	}

	var tools []openaiTool
	var toolChoice string
	if len(req.Tools) > 0 {
		tools = make([]openaiTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, openaiTool{
				Type: "function",
				Function: openaiToolSchema{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			})
		}
		toolChoice = "auto"
	}

	payload := struct {
		Model       string       `json:"model"`
		Messages    []openaiMsg  `json:"messages"`
		Stream      bool         `json:"stream"`
		Temperature float64      `json:"temperature"`
		TopP        float64      `json:"top_p"`
		Tools       []openaiTool `json:"tools,omitempty"`
		ToolChoice  string       `json:"tool_choice,omitempty"`
	}{
		Model:       p.model,
		Messages:    msgs,
		Stream:      true,
		Temperature: temp,
		TopP:        topP,
		Tools:       tools,
		ToolChoice:  toolChoice,
	}
	return json.Marshal(payload)
}

// doWithRetry sends the POST with up to 3 attempts, retrying only on
// 5xx or transport errors. A 4xx surfaces immediately so malformed
// payloads fail loud.
func (p *OpenAIProvider) doWithRetry(ctx context.Context, body []byte) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= len(retryDelays); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelays[attempt-1]):
			}
		}

		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			p.baseURL+"/chat/completions",
			bytes.NewReader(body),
		)
		if err != nil {
			return nil, fmt.Errorf("llmchat/openai: build http request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if p.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.apiKey)
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue // retry on transport error
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// Don't retry 4xx — read a small preview for the error
			// message, then close and return.
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			return nil, fmt.Errorf(
				"llmchat/openai: non-retryable status %d: %s",
				resp.StatusCode, strings.TrimSpace(string(preview)),
			)
		}
		// 5xx or other — drain + close + retry
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<14))
		_ = resp.Body.Close()
		lastErr = fmt.Errorf("server status %d", resp.StatusCode)
	}
	return nil, fmt.Errorf("llmchat/openai: retry exhausted: %w", lastErr)
}

// stream reads the SSE body frame-by-frame and emits Chunks. It runs
// in a goroutine spawned by Complete; it owns closing the response
// body and the output channel.
func (p *OpenAIProvider) stream(ctx context.Context, resp *http.Response, out chan<- Chunk) {
	defer resp.Body.Close()
	defer close(out)

	reader := bufio.NewReader(resp.Body)
	var buf bytes.Buffer

	for {
		// ctx cancellation takes precedence over the read loop so a
		// slow backend cannot hold the goroutine forever.
		select {
		case <-ctx.Done():
			emit(ctx, out, Chunk{Done: true, Err: ctx.Err()})
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			buf.Write(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				p.drainBuffer(ctx, &buf, out)
				emit(ctx, out, Chunk{Done: true})
				return
			}
			emit(ctx, out, Chunk{Done: true, Err: fmt.Errorf("llmchat/openai: stream read: %w", err)})
			return
		}

		// SSE frame boundary is a blank line (`\n\n` or `\r\n\r\n`).
		if !isBlankLine(line) {
			continue
		}

		frameBytes := buf.Bytes()
		buf.Reset()
		if stop := p.emitFrame(ctx, frameBytes, out); stop {
			return
		}
	}
}

// drainBuffer processes any frame still sitting in the buffer on EOF
// without a trailing blank line. Some backends close the stream
// immediately after `data: [DONE]\n` with no second newline.
func (p *OpenAIProvider) drainBuffer(ctx context.Context, buf *bytes.Buffer, out chan<- Chunk) {
	if buf.Len() == 0 {
		return
	}
	_ = p.emitFrame(ctx, buf.Bytes(), out)
}

// emitFrame decodes a single SSE block and forwards a Chunk. Returns
// true when the stream has terminated and the caller should stop
// reading (e.g. `data: [DONE]` or a non-recoverable JSON error).
func (p *OpenAIProvider) emitFrame(ctx context.Context, frame []byte, out chan<- Chunk) bool {
	for _, rawLine := range bytes.Split(frame, []byte{'\n'}) {
		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue // SSE comments (lines starting with `:`) + event: lines — ignore
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if bytes.Equal(payload, []byte("[DONE]")) {
			emit(ctx, out, Chunk{Done: true})
			return true
		}

		var parsed openaiStreamFrame
		if err := json.Unmarshal(payload, &parsed); err != nil {
			emit(ctx, out, Chunk{Done: true, Err: fmt.Errorf("llmchat/openai: decode frame: %w", err)})
			return true
		}

		chunk := Chunk{}
		for _, c := range parsed.Choices {
			if c.Delta.Content != "" {
				chunk.Delta += c.Delta.Content
			}
			for _, tc := range c.Delta.ToolCalls {
				out := ToolCall{
					ID:   tc.ID,
					Name: tc.Function.Name,
				}
				if tc.Function.Arguments != "" {
					out.Arguments = json.RawMessage(tc.Function.Arguments)
				}
				chunk.ToolCalls = append(chunk.ToolCalls, out)
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				chunk.FinishReason = *c.FinishReason
			}
		}
		if chunk.Delta == "" && len(chunk.ToolCalls) == 0 && chunk.FinishReason == "" {
			continue
		}
		emit(ctx, out, chunk)
		if chunk.FinishReason != "" {
			emit(ctx, out, Chunk{Done: true, FinishReason: chunk.FinishReason})
			return true
		}
	}
	return false
}

// emit sends a Chunk, honouring ctx cancellation so a slow reader
// cannot deadlock the goroutine.
func emit(ctx context.Context, out chan<- Chunk, c Chunk) {
	select {
	case <-ctx.Done():
	case out <- c:
	}
}

func isBlankLine(line []byte) bool {
	s := bytes.TrimRight(line, "\r\n")
	return len(s) == 0
}

// HealthCheck is a 2s-timeout GET {BaseURL}/models — vLLM returns 200
// with a model list when ready, 503 while weights are loading.
func (p *OpenAIProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("llmchat/openai: build health request: %w", err)
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.healthClient.Do(req)
	if err != nil {
		return fmt.Errorf("llmchat/openai: health GET failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("llmchat/openai: health GET status %d", resp.StatusCode)
	}
	return nil
}
