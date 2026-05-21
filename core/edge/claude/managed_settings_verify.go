package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Loopback-URL validation sentinels. Each violation class returns a unique
// sentinel so tests assert via errors.Is rather than brittle error-text
// substring matches and so operator log analyzers can dispatch on category.
var (
	errAgentdURLEmpty    = errors.New("CORDUM_AGENTD_URL is empty")
	errAgentdURLScheme   = errors.New("CORDUM_AGENTD_URL: scheme must be http or https")
	errAgentdURLUserinfo = errors.New("CORDUM_AGENTD_URL: userinfo credentials not permitted on loopback URL")
	errAgentdURLHost     = errors.New("CORDUM_AGENTD_URL: host must be a loopback address (127.0.0.1, ::1, or localhost)")
	errAgentdURLPort     = errors.New("CORDUM_AGENTD_URL: explicit port in range 1-65535 required")
	errAgentdURLPath     = errors.New("CORDUM_AGENTD_URL: path must be /v1/edge/hooks/claude")
	errAgentdURLQuery    = errors.New("CORDUM_AGENTD_URL: query string not permitted")
	errAgentdURLFragment = errors.New("CORDUM_AGENTD_URL: fragment not permitted")
)

// maxManagedSettingsFileBytes caps the size we will read from a managed
// settings file. Operators upload these via MDM/Jamf/Intune; legitimate
// payloads are well under 50 KiB. The 1 MiB ceiling protects the verify
// path from OOM if an attacker swaps the file for an arbitrarily large one.
const maxManagedSettingsFileBytes int64 = 1 << 20

// Severity strings exported alongside ManagedSettingsDrift so consumers can
// branch on them without importing internal constants.
const (
	managedSettingsDriftCritical = "critical"
	managedSettingsDriftHigh     = "high"
)

// ManagedSettingsDrift describes a single invariant violation discovered by
// VerifyManagedSettings. Field names use dotted/bracketed paths
// ("hooks.PreToolUse[0].command") to point operators at the broken node.
type ManagedSettingsDrift struct {
	Field    string `json:"field"`
	Got      string `json:"got"`
	Want     string `json:"want"`
	Severity string `json:"severity"`
}

// ManagedSettingsVerifyResult is the outcome of a single verify call. OK is
// true only when Drifts is empty. Source is opaque metadata describing where
// the verified bytes came from (e.g. "managed-settings.json"); callers use
// it for diagnostics only.
type ManagedSettingsVerifyResult struct {
	OK     bool                   `json:"ok"`
	Drifts []ManagedSettingsDrift `json:"drifts"`
	Source string                 `json:"source"`
}

// VerifyManagedSettings parses a managed-settings.json payload and reports
// every Cordum Edge invariant violation. A nil error indicates the payload
// was structurally valid JSON; check ManagedSettingsVerifyResult.OK to
// learn whether the contents matched the enterprise contract.
func VerifyManagedSettings(data []byte) (ManagedSettingsVerifyResult, error) {
	var doc managedSettingsDocument
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		// Allow extra/unknown fields by retrying without strict mode so a
		// future Claude Code update that introduces a new optional key does
		// not break verify; only structural decode errors propagate.
		dec2 := json.NewDecoder(strings.NewReader(string(data)))
		if err2 := dec2.Decode(&doc); err2 != nil {
			return ManagedSettingsVerifyResult{}, fmt.Errorf("parse managed-settings.json: %w", err2)
		}
	}
	drifts := collectManagedSettingsDrifts(doc, data)
	return ManagedSettingsVerifyResult{
		OK:     len(drifts) == 0,
		Drifts: drifts,
		Source: "managed-settings.json",
	}, nil
}

// VerifyManagedSettingsFromPath opens path with a 1 MiB cap and runs
// VerifyManagedSettings on the contents. Returns a non-nil error for IO
// failures, oversized files, or unparseable JSON.
func VerifyManagedSettingsFromPath(path string) (ManagedSettingsVerifyResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return ManagedSettingsVerifyResult{}, fmt.Errorf("open managed-settings.json: %w", err)
	}
	defer func() { _ = f.Close() }()
	limited := io.LimitReader(f, maxManagedSettingsFileBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return ManagedSettingsVerifyResult{}, fmt.Errorf("read managed-settings.json: %w", err)
	}
	if int64(len(data)) > maxManagedSettingsFileBytes {
		return ManagedSettingsVerifyResult{}, fmt.Errorf("managed-settings.json exceeds %d bytes", maxManagedSettingsFileBytes)
	}
	res, err := VerifyManagedSettings(data)
	if err != nil {
		return ManagedSettingsVerifyResult{}, err
	}
	res.Source = path
	return res, nil
}

// requiredManagedHookEvents is the canonical set of hook events that must be
// present (each with at least one non-empty command). Order is preserved for
// deterministic drift reporting.
var requiredManagedHookEvents = []string{
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"UserPromptSubmit",
	"ConfigChange",
	"FileChanged",
}

var requiredManagedHookSubcommands = map[string]string{
	"PreToolUse":         "pre-tool-use",
	"PostToolUse":        "post-tool-use",
	"PostToolUseFailure": "post-tool-use-failure",
	"UserPromptSubmit":   "user-prompt-submit",
	"ConfigChange":       "config-change",
	"FileChanged":        "file-changed",
}

var canonicalManagedHookCommands = []string{
	"/opt/cordum/bin/cordum-hook",
	"/usr/local/bin/cordum-hook",
	"/Applications/Cordum Edge/cordum-hook",
	`C:\Program Files\Cordum\cordum-hook.exe`,
}

// reservedManagedEnvKeys lists env vars that MUST NOT appear in managed
// settings. The bundle generator never emits them; their presence indicates
// an operator hand-edited a secret into the file.
var reservedManagedEnvKeys = []string{
	"CORDUM_AGENTD_HOOK_NONCE",
	"ANTHROPIC_API_KEY",
	"CORDUM_API_KEY",
}

// sensitiveSerializedMarkers are byte sequences that indicate a leaked
// secret regardless of which JSON key carries them. Mirror upstream
// containsSensitiveValue + add explicit auth-header marker so a
// stringified Authorization header in a debug env var still trips.
var sensitiveSerializedMarkers = []string{
	"sk-",
	"ghp_",
	"Authorization: Bearer",
	"AKIA",
}

func collectManagedSettingsDrifts(doc managedSettingsDocument, raw []byte) []ManagedSettingsDrift {
	var drifts []ManagedSettingsDrift
	if !doc.AllowManagedHooksOnly {
		drifts = append(drifts, ManagedSettingsDrift{Field: "allowManagedHooksOnly", Got: "false", Want: "true", Severity: managedSettingsDriftCritical})
	}
	if !doc.AllowManagedMcpServersOnly {
		drifts = append(drifts, ManagedSettingsDrift{Field: "allowManagedMcpServersOnly", Got: "false", Want: "true", Severity: managedSettingsDriftCritical})
	}
	if doc.DisableBypassPermissions != "disable" {
		drifts = append(drifts, ManagedSettingsDrift{
			Field:    "disableBypassPermissionsMode",
			Got:      redactDiagnostic(doc.DisableBypassPermissions),
			Want:     "disable",
			Severity: managedSettingsDriftCritical,
		})
	}
	// Production-boundary checks intentionally have no settings-file flag
	// bypass. A future exception must be a separately verified, signed
	// trusted-config input; config bytes alone are not trusted to weaken
	// HTTP hook, MCP allowlist, or hook-command boundaries.
	drifts = append(drifts, allowedHTTPHookURLDrifts(doc.AllowedHTTPHookURLs)...)
	drifts = append(drifts, allowedMCPServerDrifts(doc.AllowedMcpServers)...)
	drifts = append(drifts, requiredHookEventDrifts(doc.Hooks)...)
	drifts = append(drifts, requiredEnvDrifts(doc.Env)...)
	drifts = append(drifts, reservedEnvDrifts(doc.Env)...)
	drifts = append(drifts, agentdURLDrifts(doc.Env)...)
	drifts = append(drifts, serializedMarkerDrifts(raw)...)
	return drifts
}

func allowedHTTPHookURLDrifts(urls []string) []ManagedSettingsDrift {
	if len(urls) == 0 {
		return nil
	}
	return []ManagedSettingsDrift{{
		Field:    "allowedHttpHookUrls",
		Got:      fmt.Sprintf("%d configured", len(urls)),
		Want:     "empty list; HTTP hooks are not a production boundary",
		Severity: managedSettingsDriftCritical,
	}}
}

func allowedMCPServerDrifts(servers []managedMCPAllow) []ManagedSettingsDrift {
	if len(servers) != 1 {
		return []ManagedSettingsDrift{{
			Field:    "allowedMcpServers",
			Got:      fmt.Sprintf("%d configured", len(servers)),
			Want:     "exactly cordum-edge",
			Severity: managedSettingsDriftCritical,
		}}
	}
	server := servers[0]
	if strings.TrimSpace(server.ServerName) != "cordum-edge" {
		return []ManagedSettingsDrift{{
			Field:    "allowedMcpServers[0].serverName",
			Got:      redactDiagnostic(server.ServerName),
			Want:     "cordum-edge",
			Severity: managedSettingsDriftCritical,
		}}
	}
	if strings.TrimSpace(server.Command) != "" || len(server.Args) > 0 {
		return []ManagedSettingsDrift{{
			Field:    "allowedMcpServers[0]",
			Got:      "runtime command fields present",
			Want:     "serverName only",
			Severity: managedSettingsDriftCritical,
		}}
	}
	return nil
}

func requiredHookEventDrifts(hooks map[string][]claudeHookSet) []ManagedSettingsDrift {
	var drifts []ManagedSettingsDrift
	for _, event := range requiredManagedHookEvents {
		sets, ok := hooks[event]
		if !ok || len(sets) == 0 {
			drifts = append(drifts, ManagedSettingsDrift{
				Field:    "hooks." + event,
				Got:      "missing",
				Want:     "at least one command hook",
				Severity: managedSettingsDriftCritical,
			})
			continue
		}
		if !hasCanonicalManagedHookCommand(event, sets) {
			drifts = append(drifts, ManagedSettingsDrift{
				Field:    "hooks." + event + "[0].command",
				Got:      "missing or non-canonical command",
				Want:     "canonical cordum-hook command",
				Severity: managedSettingsDriftCritical,
			})
		}
	}
	return drifts
}

func hasCanonicalManagedHookCommand(event string, sets []claudeHookSet) bool {
	subcommand := requiredManagedHookSubcommands[event]
	for _, set := range sets {
		for _, hook := range set.Hooks {
			if isCanonicalManagedHookCommand(hook.Command, subcommand) {
				return true
			}
		}
	}
	return false
}

func isCanonicalManagedHookCommand(command, subcommand string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" || subcommand == "" {
		return false
	}
	for _, hookPath := range canonicalManagedHookCommands {
		if trimmed == commandHook(hookPath, subcommand, 0).Command && canonicalManagedHookPathAllowed(hookPath) {
			return true
		}
	}
	return false
}

func canonicalManagedHookPathAllowed(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return true
	}
	if sameManagedHookPath(resolved, path) {
		return true
	}
	for _, allowed := range canonicalManagedHookCommands {
		if sameManagedHookPath(resolved, allowed) {
			return true
		}
	}
	return false
}

func sameManagedHookPath(a, b string) bool {
	return filepath.Clean(strings.TrimSpace(a)) == filepath.Clean(strings.TrimSpace(b))
}

func requiredEnvDrifts(env map[string]string) []ManagedSettingsDrift {
	required := []struct {
		key      string
		want     string
		severity string
	}{
		{key: "CORDUM_AGENTD_FAIL_CLOSED", want: "true", severity: managedSettingsDriftCritical},
		{key: "CORDUM_EDGE_MANAGED_POLICY_MODE", want: "enterprise-strict", severity: managedSettingsDriftCritical},
		{key: "CORDUM_EDGE_MANAGED_HOOKS_ONLY", want: "true", severity: managedSettingsDriftCritical},
	}
	var drifts []ManagedSettingsDrift
	for _, r := range required {
		got := env[r.key]
		if got != r.want {
			drifts = append(drifts, ManagedSettingsDrift{
				Field:    "env." + r.key,
				Got:      redactDiagnostic(got),
				Want:     r.want,
				Severity: r.severity,
			})
		}
	}
	return drifts
}

func reservedEnvDrifts(env map[string]string) []ManagedSettingsDrift {
	var drifts []ManagedSettingsDrift
	for _, key := range reservedManagedEnvKeys {
		if _, ok := env[key]; ok {
			drifts = append(drifts, ManagedSettingsDrift{
				Field:    "env." + key,
				Got:      "present",
				Want:     "absent",
				Severity: managedSettingsDriftHigh,
			})
		}
	}
	// Catch any env value that contains a sensitive marker (covers
	// debug/custom keys not in the reserved list).
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if isReservedEnvKey(key) {
			continue
		}
		if containsSensitiveValue(env[key]) {
			drifts = append(drifts, ManagedSettingsDrift{
				Field:    "env." + key,
				Got:      "[REDACTED]",
				Want:     "non-sensitive value",
				Severity: managedSettingsDriftHigh,
			})
		}
	}
	return drifts
}

func isReservedEnvKey(key string) bool {
	return slices.Contains(reservedManagedEnvKeys, key)
}

// validateLoopbackAgentdURL enforces the loopback-only contract for the
// CORDUM_AGENTD_URL env var. Operators provision this URL via managed
// settings (Jamf/Intune/MDM) and a non-loopback value would route hook
// decisions off-box — a SSRF target on a misconfigured fleet host, or a
// silent man-in-the-middle for enforcement decisions. The function rejects:
//
//   - empty / whitespace-only string (errAgentdURLEmpty)
//   - unparseable URL or missing/unsupported scheme (errAgentdURLScheme)
//   - any userinfo, even username-only (errAgentdURLUserinfo)
//   - host outside {127.0.0.1, ::1, localhost} (errAgentdURLHost)
//   - missing/non-numeric/out-of-range port (errAgentdURLPort)
//   - path != /v1/edge/hooks/claude (errAgentdURLPath)
//   - any RawQuery (errAgentdURLQuery)
//   - any Fragment (errAgentdURLFragment)
//
// Each sentinel is exported within-package so tests assert via errors.Is
// instead of brittle error-text substring matches. nil indicates the URL
// satisfies every loopback invariant.
func validateLoopbackAgentdURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return errAgentdURLEmpty
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		// Parse errors here are dominated by missing- or malformed-scheme
		// values (`127.0.0.1:8765/...` triggers url.Parse to fail when the
		// scheme prefix is not a valid RFC 3986 alpha-leading scheme).
		// Surface them under the scheme sentinel so the operator gets a
		// single category to remediate instead of a noisy split.
		return errAgentdURLScheme
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errAgentdURLScheme
	}
	// User MUST appear NOWHERE in a loopback hook URL. Even username-only
	// (no password) is an antipattern: credentials encoded in URLs are
	// frequently echoed into logs and tcpdump captures.
	if parsed.User != nil {
		return errAgentdURLUserinfo
	}
	host := parsed.Hostname()
	if !isLoopbackHookHost(host) {
		return errAgentdURLHost
	}
	port := parsed.Port()
	if port == "" {
		return errAgentdURLPort
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return errAgentdURLPort
	}
	if parsed.Path != "/v1/edge/hooks/claude" {
		return errAgentdURLPath
	}
	if strings.TrimSpace(parsed.RawQuery) != "" {
		return errAgentdURLQuery
	}
	if strings.TrimSpace(parsed.Fragment) != "" {
		return errAgentdURLFragment
	}
	return nil
}

// agentdURLDriftLabels maps each validation sentinel to a (got, want) pair
// suitable for an operator-facing ManagedSettingsDrift. Each Got is a fixed
// safe label so userinfo, query payload, or fragment text CANNOT leak from
// raw input into the diagnostic output.
func agentdURLDriftLabels(err error) (got, want string) {
	switch {
	case errors.Is(err, errAgentdURLEmpty):
		return "empty", "loopback hook URL"
	case errors.Is(err, errAgentdURLScheme):
		return "unparseable URL or unsupported scheme", "http(s) loopback URL"
	case errors.Is(err, errAgentdURLUserinfo):
		return "url with userinfo credentials", "url without userinfo"
	case errors.Is(err, errAgentdURLHost):
		return "non-loopback host", "loopback host (127.0.0.1, ::1, or localhost)"
	case errors.Is(err, errAgentdURLPort):
		return "missing or invalid port", "explicit port 1-65535"
	case errors.Is(err, errAgentdURLPath):
		return "wrong path", "path /v1/edge/hooks/claude"
	case errors.Is(err, errAgentdURLQuery):
		return "url with query string", "url without query string"
	case errors.Is(err, errAgentdURLFragment):
		return "url with fragment", "url without fragment"
	default:
		return "invalid", "valid loopback hook URL"
	}
}

func agentdURLDrifts(env map[string]string) []ManagedSettingsDrift {
	raw := env["CORDUM_AGENTD_URL"]
	if err := validateLoopbackAgentdURL(raw); err != nil {
		got, want := agentdURLDriftLabels(err)
		return []ManagedSettingsDrift{{
			Field:    "env.CORDUM_AGENTD_URL",
			Got:      got,
			Want:     want,
			Severity: managedSettingsDriftCritical,
		}}
	}
	return nil
}

func isLoopbackHookHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "127.0.0.1", "::1", "localhost":
		return true
	default:
		return false
	}
}

func serializedMarkerDrifts(raw []byte) []ManagedSettingsDrift {
	body := string(raw)
	for _, marker := range sensitiveSerializedMarkers {
		if strings.Contains(body, marker) {
			return []ManagedSettingsDrift{{
				Field:    "serialized.sensitive_marker",
				Got:      marker,
				Want:     "no leaked secret markers",
				Severity: managedSettingsDriftHigh,
			}}
		}
	}
	return nil
}
