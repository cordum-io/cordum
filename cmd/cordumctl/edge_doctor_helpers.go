package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type edgeDoctorSettingsDoc struct {
	Env   map[string]string          `json:"env"`
	Hooks map[string][]edgeHookGroup `json:"hooks"`
}

type edgeHookGroup struct {
	Hooks []edgeCommandHook `json:"hooks"`
}

type edgeCommandHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func defaultEdgeDoctorDialTCP(ctx context.Context, address string) error {
	dialCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", address)
	if err != nil {
		return err
	}
	return conn.Close()
}

func edgeCheckExecutable(env *edgeDoctorEnv, label, configured, fallback, fix string) checkResult {
	command := strings.TrimSpace(configured)
	if command == "" {
		command = fallback
	}
	path, err := edgeResolveExecutable(env, command)
	if err != nil {
		return checkResult{State: stateFail, Detail: label + " not found: " + err.Error(), Fix: fix}
	}
	return checkResult{State: stateOK, Detail: label + " resolved at " + path}
}

func edgeResolveExecutable(env *edgeDoctorEnv, command string) (string, error) {
	if edgeLooksLikePath(command) {
		info, err := env.statFile(command)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory", command)
		}
		return command, nil
	}
	path, err := env.lookPath(command)
	if err != nil {
		return "", err
	}
	return path, nil
}

func edgeLooksLikePath(command string) bool {
	return filepath.IsAbs(command) || strings.ContainsAny(command, `/\`)
}

func edgeDoctorCommandExecutable(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, `"`) {
		if end := strings.Index(trimmed[1:], `"`); end >= 0 {
			return trimmed[1 : end+1]
		}
	}
	if strings.HasPrefix(trimmed, `'`) {
		if end := strings.Index(trimmed[1:], `'`); end >= 0 {
			return trimmed[1 : end+1]
		}
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func validateEdgeDoctorSettings(data []byte) error {
	var doc edgeDoctorSettingsDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("settings JSON invalid: %w", err)
	}
	if err := validateEdgeSettingsEnv(doc.Env); err != nil {
		return err
	}
	if err := validateEdgeSettingsHooks(doc.Hooks); err != nil {
		return err
	}
	return nil
}

func validateEdgeSettingsEnv(env map[string]string) error {
	if len(env) == 0 {
		return fmt.Errorf("env block missing")
	}
	for _, key := range []string{"CORDUM_API_KEY", "CORDUM_AGENTD_NONCE", "CORDUM_AGENTD_HOOK_NONCE"} {
		if _, ok := env[key]; ok {
			return fmt.Errorf("settings persists forbidden secret env %s", key)
		}
	}
	for key := range env {
		if edgeDoctorSensitiveEnvKey(key) {
			return fmt.Errorf("settings persists sensitive env key %s", key)
		}
	}
	agentdURL := strings.TrimSpace(env["CORDUM_AGENTD_URL"])
	if agentdURL == "" && strings.TrimSpace(env["CORDUM_AGENTD_SOCKET"]) == "" {
		return fmt.Errorf("CORDUM_AGENTD_URL or CORDUM_AGENTD_SOCKET missing")
	}
	if strings.Contains(strings.ToLower(agentdURL), "nonce=") {
		return fmt.Errorf("CORDUM_AGENTD_URL must not persist nonce query parameters")
	}
	if strings.TrimSpace(env["CORDUM_EDGE_SESSION_ID"]) == "" || strings.TrimSpace(env["CORDUM_EDGE_EXECUTION_ID"]) == "" {
		return fmt.Errorf("session/execution metadata missing")
	}
	return nil
}

func edgeDoctorSensitiveEnvKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if strings.Contains(k, "api_key") || strings.Contains(k, "apikey") {
		return true
	}
	for _, marker := range []string{"password", "passwd", "secret", "token", "credential", "nonce"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func validateEdgeSettingsHooks(hooks map[string][]edgeHookGroup) error {
	required := []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "ConfigChange", "FileChanged"}
	for _, event := range required {
		if !edgeHookEventHasCommand(hooks[event]) {
			return fmt.Errorf("missing Cordum command hook for %s", event)
		}
	}
	return nil
}

func edgeHookEventHasCommand(groups []edgeHookGroup) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Type == "command" && strings.Contains(hook.Command, "cordum-hook") {
				return true
			}
		}
	}
	return false
}

func edgeLoopbackHost(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid agentd URL")
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("agentd URL must use local http loopback")
	}
	if !edgeIsLoopbackHost(u.Hostname()) {
		return "", fmt.Errorf("agentd URL must be loopback, got host %q", u.Hostname())
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("agentd URL missing host")
	}
	return u.Host, nil
}

func edgeIsLoopbackHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func edgeModeImplication(mode string) string {
	switch edgePolicyModeOrDefault(mode) {
	case "observe":
		return "observe mode degrades open"
	case "enterprise-strict":
		return "enterprise-strict fails closed"
	default:
		return "enforce mode denies risky/unknown degraded actions"
	}
}

func edgePolicyModeOrDefault(mode string) string {
	if trimmed := strings.TrimSpace(mode); trimmed != "" {
		return trimmed
	}
	return "enforce"
}

func edgeDoctorHasAPIKey(env *edgeDoctorEnv) bool {
	return env != nil && env.base != nil && strings.TrimSpace(env.base.apiKey) != ""
}

func edgeSafeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

func edgeDoctorRedact(message, apiKey string) string {
	return redactEdgeClaudeError(message, apiKey)
}

func edgeDoctorNetworkError(err error) string {
	if err == nil {
		return "request failed"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	case strings.Contains(msg, "no such host"):
		return "host not found"
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls"):
		return "TLS error"
	default:
		return "network error"
	}
}
