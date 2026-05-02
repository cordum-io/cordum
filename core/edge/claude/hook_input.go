package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

const (
	DefaultMaxInputBytes int64 = 1 << 20

	// ClaudeHookDeadline is Claude Code's documented per-hook command deadline.
	ClaudeHookDeadline time.Duration = 5 * time.Second
	// DefaultHookTimeout MUST stay under Claude Code's 5s hook deadline
	// (PRD §7.4); runner.go splits it into agentd-POST + response-write budgets.
	DefaultHookTimeout      time.Duration = 4500 * time.Millisecond
	DefaultAgentdPostBudget time.Duration = 4 * time.Second
	ResponseWriteReserve    time.Duration = 500 * time.Millisecond
)

var (
	errEmptyInput    = errors.New("empty hook input")
	errMalformedJSON = errors.New("invalid hook json")
	errInputTooLarge = errors.New("hook input too large")
	errMultipleJSON  = errors.New("multiple json values")
	errNonObjectJSON = errors.New("hook input must be a json object")
	errInputTimeout  = errors.New("hook input timeout")
)

// RunOptions wires command-hook I/O into the Claude hook runner.
type RunOptions struct {
	Args          []string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	Env           map[string]string
	Agentd        AgentdClient
	Recorder      edgecore.Recorder
	MaxInputBytes int64
	Timeout       time.Duration
	// AgentdPostBudget caps only the local agentd POST; it is clamped so the
	// hook retains time to serialize and write Claude's response.
	AgentdPostBudget time.Duration
}

// HookInput contains the Claude Code hook fields needed by EDGE-015. RawPayload
// is retained only in memory for forwarding to the local agentd.
type HookInput struct {
	SessionID      string         `json:"session_id,omitempty"`
	TranscriptPath string         `json:"transcript_path,omitempty"`
	CWD            string         `json:"cwd,omitempty"`
	PermissionMode string         `json:"permission_mode,omitempty"`
	HookEventName  string         `json:"hook_event_name,omitempty"`
	ToolName       string         `json:"tool_name,omitempty"`
	ToolInput      map[string]any `json:"tool_input,omitempty"`
	ToolResponse   map[string]any `json:"tool_response,omitempty"`
	ToolUseID      string         `json:"tool_use_id,omitempty"`
	DurationMS     int            `json:"duration_ms,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	Error          string         `json:"error,omitempty"`
	Source         string         `json:"source,omitempty"`
	FilePath       string         `json:"file_path,omitempty"`
	FileEvent      string         `json:"event,omitempty"`
	IsInterrupt    bool           `json:"is_interrupt,omitempty"`
	RawPayload     []byte         `json:"-"`
}

func readHookInput(ctx context.Context, r io.Reader, maxBytes int64) (HookInput, error) {
	if r == nil {
		return HookInput{}, errEmptyInput
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxInputBytes
	}
	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		limited := io.LimitReader(r, maxBytes+1)
		data, err := io.ReadAll(limited)
		ch <- readResult{data: data, err: err}
	}()
	select {
	case <-ctx.Done():
		if closer, ok := r.(io.Closer); ok {
			_ = closer.Close()
		}
		return HookInput{}, errInputTimeout
	case res := <-ch:
		if res.err != nil {
			return HookInput{}, fmt.Errorf("read hook input: %w", res.err)
		}
		if int64(len(res.data)) > maxBytes {
			return HookInput{}, errInputTooLarge
		}
		if len(bytes.TrimSpace(res.data)) == 0 {
			return HookInput{}, errEmptyInput
		}
		return parseHookInput(res.data)
	}
}

func parseHookInput(data []byte) (HookInput, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var probe any
	if err := dec.Decode(&probe); err != nil {
		return HookInput{}, errMalformedJSON
	}
	if _, ok := probe.(map[string]any); !ok {
		return HookInput{}, errNonObjectJSON
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return HookInput{}, errMultipleJSON
	}

	dec = json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var input HookInput
	if err := dec.Decode(&input); err != nil {
		return HookInput{}, errMalformedJSON
	}
	input.RawPayload = append([]byte(nil), data...)
	return input, nil
}

func maxInputBytes(opts RunOptions) int64 {
	if opts.MaxInputBytes > 0 {
		return opts.MaxInputBytes
	}
	if raw := envValue(opts.Env, "CORDUM_HOOK_MAX_INPUT_BYTES"); raw != "" {
		if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && n > 0 && n <= 8*DefaultMaxInputBytes {
			return n
		}
	}
	return DefaultMaxInputBytes
}

func hookTimeout(opts RunOptions) (time.Duration, error) {
	if opts.Timeout > 0 {
		return validateHookTimeout("RunOptions.Timeout", opts.Timeout)
	}
	if raw := envValue(opts.Env, "CORDUM_AGENTD_HOOK_TIMEOUT"); raw != "" {
		if d, ok := parseHookTimeout(raw); ok {
			return validateHookTimeout("CORDUM_AGENTD_HOOK_TIMEOUT", d)
		}
	}
	return DefaultHookTimeout, nil
}

func parseHookTimeout(raw string) (time.Duration, bool) {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d, true
	}
	if secs, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second)), true
	}
	return 0, false
}

func validateHookTimeout(source string, d time.Duration) (time.Duration, error) {
	if d >= ClaudeHookDeadline {
		return 0, fmt.Errorf("%s=%s must stay strictly below Claude Code's %s hook deadline", source, d, ClaudeHookDeadline)
	}
	return d, nil
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return strings.TrimSpace(env[key])
	}
	return strings.TrimSpace(getenv(key))
}

var getenv = os.Getenv
