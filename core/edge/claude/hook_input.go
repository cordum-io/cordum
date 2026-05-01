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
)

const (
	DefaultMaxInputBytes int64         = 1 << 20
	DefaultHookTimeout   time.Duration = 10 * time.Second
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
	MaxInputBytes int64
	Timeout       time.Duration
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

func hookTimeout(opts RunOptions) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	if raw := envValue(opts.Env, "CORDUM_AGENTD_HOOK_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
		if secs, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
	}
	return DefaultHookTimeout
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return strings.TrimSpace(env[key])
	}
	return strings.TrimSpace(getenv(key))
}

var getenv = os.Getenv
