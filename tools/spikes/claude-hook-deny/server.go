package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	hookPath                      = "/hook/pre-tool-use"
	hookListenAddress             = "127.0.0.1:7777"
	maxHookRequestBytes           = 1 << 20
	serverReadHeaderTimeout       = 5 * time.Second
	recursiveDeleteDenialReason   = "Cordum policy blocked this Bash command: destructive recursive deletion is not allowed."
	maxLoggedFieldRunes           = 80
	maxLoggedSessionRunes         = 16
	redactedEmptyLogField         = "-"
	redactedSensitiveLogField     = "[redacted]"
	destructiveCommandCategory    = "rm_recursive_force"
	nonDestructiveCommandCategory = "none"
)

type HookInput struct {
	HookEventName string                 `json:"hook_event_name"`
	SessionID     string                 `json:"session_id"`
	CWD           string                 `json:"cwd"`
	ToolName      string                 `json:"tool_name"`
	ToolUseID     string                 `json:"tool_use_id"`
	ToolInput     map[string]interface{} `json:"tool_input"`
}

type HookDecision struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func main() {
	server := &http.Server{
		Addr:              hookListenAddress,
		Handler:           newHookMux(),
		ReadHeaderTimeout: serverReadHeaderTimeout,
	}

	log.Printf("Cordum hook spike listening on http://%s", hookListenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Cordum hook spike failed: %v", err)
	}
}

func newHookMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(hookPath, preToolUseHandler)
	return mux
}

func preToolUseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxHookRequestBytes)
	defer func() { _ = r.Body.Close() }()

	var in HookInput
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&in); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "hook input too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad hook input", http.StatusBadRequest)
		return
	}

	command, _ := in.ToolInput["command"].(string)
	destructive := in.ToolName == "Bash" && isDestructiveRecursiveDelete(command)
	logHookInput(in, destructive)

	if destructive {
		out := newDenyDecision(recursiveDeleteDenialReason)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func newDenyDecision(reason string) HookDecision {
	var out HookDecision
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = reason
	return out
}

func isDestructiveRecursiveDelete(command string) bool {
	fields := strings.Fields(command)
	for i, field := range fields {
		// Match by basename so absolute paths like /bin/rm and /usr/bin/rm
		// still trigger the denial. Without this, a caller could trivially
		// bypass by writing the full path to rm.
		if commandBasename(normalizeShellToken(field)) != "rm" || !isCommandPosition(fields, i) {
			continue
		}

		flags := ""
		for _, arg := range fields[i+1:] {
			normalized := normalizeShellToken(arg)
			if !strings.HasPrefix(normalized, "-") || normalized == "-" {
				break
			}
			flags += strings.TrimLeft(normalized, "-")
		}

		flags = strings.ToLower(flags)
		if strings.Contains(flags, "r") && strings.Contains(flags, "f") {
			return true
		}
	}

	return false
}

func isCommandPosition(fields []string, index int) bool {
	if index == 0 {
		return true
	}

	previous := normalizeShellToken(fields[index-1])
	if previous == "sudo" || previous == "doas" || previous == "command" {
		return true
	}
	if isShellEvalPosition(fields, index) {
		return true
	}

	rawPrevious := strings.TrimSpace(fields[index-1])
	return rawPrevious == "&&" || rawPrevious == "||" || rawPrevious == "|" || strings.HasSuffix(rawPrevious, ";")
}

func isShellEvalPosition(fields []string, index int) bool {
	if index < 2 {
		return false
	}

	previous := normalizeShellToken(fields[index-1])
	if previous != "-c" && previous != "-lc" {
		return false
	}

	shell := normalizeShellToken(fields[index-2])
	return shell == "bash" || shell == "sh" || shell == "zsh" || shell == "dash"
}

func normalizeShellToken(token string) string {
	return strings.Trim(token, " \t\r\n;&|(){}'\"")
}

func commandBasename(token string) string {
	if i := strings.LastIndex(token, "/"); i >= 0 {
		return token[i+1:]
	}
	return token
}

func logHookInput(in HookInput, destructive bool) {
	category := nonDestructiveCommandCategory
	if destructive {
		category = destructiveCommandCategory
	}

	log.Printf(
		"event=%s session=%s tool=%s cwd=%s destructive=%t category=%s",
		boundedLogField(in.HookEventName),
		boundedSessionID(in.SessionID),
		boundedLogField(in.ToolName),
		boundedLogField(in.CWD),
		destructive,
		category,
	)
}

func boundedSessionID(value string) string {
	return boundedLogFieldWithLimit(value, maxLoggedSessionRunes)
}

func boundedLogField(value string) string {
	return boundedLogFieldWithLimit(value, maxLoggedFieldRunes)
}

func boundedLogFieldWithLimit(value string, limit int) string {
	value = sanitizeLogField(value)
	if value == "" {
		return redactedEmptyLogField
	}
	if looksTokenLike(value) {
		return redactedSensitiveLogField
	}

	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}

	return string(runes[:limit]) + "..."
}

func sanitizeLogField(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func looksTokenLike(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "authorization:") ||
		strings.Contains(lower, "api_key=") ||
		strings.Contains(lower, "api-key=") ||
		strings.Contains(lower, "token=") ||
		(strings.HasPrefix(value, "sk-") && len(value) > 20)
}
