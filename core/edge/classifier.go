package edge

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// EdgePolicyTopic is the P0 Safety Kernel topic for deterministic Edge action
// checks. Safety Kernel currently accepts job.* topics, so Edge action policy
// uses this job-prefixed topic and carries Edge dimensions in labels/metadata.
const EdgePolicyTopic = "job.edge.action"

const (
	actionUnknownHook = "unknown.hook"

	capabilityUnknown        = "edge.unknown"
	capabilityShell          = "exec.shell"
	capabilityFileRead       = "file.read"
	capabilityFileWrite      = "file.write"
	capabilityFileDelete     = "file.delete"
	capabilityFileMove       = "file.move"
	capabilityMCPMutate      = "mcp.mutate"
	capabilityMCPRead        = "mcp.read"
	capabilityLLMRequest     = "llm.request"
	capabilityRuntimeProcess = "runtime.process"
	capabilityRuntimeFile    = "runtime.file"
	capabilityRuntimeNetwork = "runtime.network"
)

// ActionClassification is the deterministic server-side classification of one
// Edge action. Client-provided risk tags are not authoritative; callers should
// use this output when constructing policy inputs. Large/raw hook payloads
// should be represented as bounded redacted content plus artifact pointers, not
// embedded in labels or logs.
//
// EDGE-069 — Complete + MissingFields capture whether the classifier's
// output is partial. A partial classification (Capability empty,
// ActionName empty, or RiskTags empty/only-default-sentinels) is
// flagged so downstream evaluators can route to deny-with-reason
// "classifier_incomplete" rather than allowing a policy match against
// a half-populated input. Backward-compat: Complete is an exported bool,
// so historic/manual callers may leave it at the zero value. Mapper
// policy labels treat zero Complete plus empty MissingFields as legacy
// unknown and recompute required-field completeness; non-empty
// MissingFields remains authoritative fail-closed evidence.
type ActionClassification struct {
	ActionName       string
	Capability       string
	RiskTags         []string
	Labels           Labels
	InputContent     []byte
	InputContentType string
	InputSizeBytes   int64
	// Complete reports whether the classifier emitted all required
	// fields (capability, action name, risk tags). false ⇒ partial
	// classification; downstream evaluators should fail closed.
	Complete bool
	// MissingFields lists the field names the classifier did NOT
	// populate when Complete=false. Sorted alphabetically for stable
	// audit-evidence emission.
	MissingFields []string
}

// ClassifyEvent normalizes an AgentActionEvent into deterministic server-side
// policy dimensions. It does not mutate the input event, does not trust
// event.RiskTags for deterministic cases, and never stores raw command strings
// or secret-like values in labels.
// hookKindRequiresTool returns true for hook kinds whose Claude Code payload
// always includes a tool_name. Other hook kinds (UserPromptSubmit, ConfigChange,
// FileChanged, PolicyDecision, PermissionRequest) carry session-level metadata
// that is intentionally tool-less; ClassifyEvent must accept them.
func hookKindRequiresTool(kind EventKind) bool {
	switch kind {
	case EventKindHookPreToolUse, EventKindHookPostToolUse, EventKindHookPostToolUseFailure:
		return true
	}
	return false
}

func ClassifyEvent(event AgentActionEvent) (ActionClassification, error) {
	if strings.TrimSpace(string(event.Layer)) == "" {
		return ActionClassification{}, fmt.Errorf("layer is required")
	}
	if strings.TrimSpace(string(event.Kind)) == "" {
		return ActionClassification{}, fmt.Errorf("kind is required")
	}
	// EDGE-049: tool_name is required ONLY for hook kinds that actually carry a
	// tool — PreToolUse, PostToolUse, PostToolUseFailure. UserPromptSubmit,
	// ConfigChange, FileChanged, PolicyDecision, PermissionRequest legitimately
	// have no tool_name and must classify cleanly. Pre-fix, every UserPromptSubmit
	// hook fell into mapper's reasonUnsupportedToolInputShape branch, agentd's
	// evaluator treated the gateway 400 as `unavailable`, and enforce mode
	// fail-closed denied every prompt — see EDGE-049 closure trail.
	if event.Layer == LayerHook && hookKindRequiresTool(event.Kind) && strings.TrimSpace(event.ToolName) == "" {
		return ActionClassification{}, fmt.Errorf("tool_name is required")
	}

	content, contentType, size, err := classifiedInputContent(event.InputRedacted)
	if err != nil {
		return ActionClassification{}, err
	}
	classification := ActionClassification{
		ActionName:       actionUnknownHook,
		Capability:       capabilityUnknown,
		RiskTags:         []string{"review_required", "unknown"},
		Labels:           baseClassificationLabels(event),
		InputContent:     content,
		InputContentType: contentType,
		InputSizeBytes:   size,
	}

	switch event.Layer {
	case LayerHook:
		classifyHookEvent(event, &classification)
	case LayerMCP:
		classifyMCPEvent(event, &classification)
	case LayerLLM:
		classifyLLMEvent(event, &classification)
	case LayerRuntime:
		classifyRuntimeEvent(event, &classification)
	default:
		classification.ActionName = "unknown." + safeLabelValue(string(event.Layer), "edge")
		classification.Capability = capabilityUnknown
		classification.RiskTags = []string{"review_required", "unknown"}
	}
	classification.RiskTags = sortedUniqueStrings(classification.RiskTags)
	classification.Labels = cloneLabels(classification.Labels)
	// EDGE-069 — flag partial classifications so downstream evaluators
	// can fail closed. A complete classification has a non-empty
	// ActionName, Capability, and RiskTags. The default-sentinel
	// fallback (capability=edge.unknown, action_name=unknown.hook,
	// risk_tags=[review_required, unknown]) is itself "complete" — the
	// classifier intentionally emits these for unknown tools so policy
	// can fire deny-unknown-high-risk. "Incomplete" means a code path
	// failed to populate one of the three required fields entirely.
	classification.Complete, classification.MissingFields = computeClassificationCompleteness(classification)
	return classification, nil
}

// computeClassificationCompleteness inspects an ActionClassification
// and reports whether the classifier produced the three required
// fields. A returned MissingFields slice is sorted (and may be nil
// when Complete=true) so audit-evidence emission is deterministic.
func computeClassificationCompleteness(c ActionClassification) (bool, []string) {
	var missing []string
	if strings.TrimSpace(c.ActionName) == "" {
		missing = append(missing, "action_name")
	}
	if strings.TrimSpace(c.Capability) == "" {
		missing = append(missing, "capability")
	}
	if len(c.RiskTags) == 0 {
		missing = append(missing, "risk_tags")
	}
	if len(missing) == 0 {
		return true, nil
	}
	sort.Strings(missing)
	return false, missing
}

func classifyHookEvent(event AgentActionEvent, out *ActionClassification) {
	toolFold := strings.ToLower(strings.TrimSpace(event.ToolName))
	// EDGE-041: cordum-hook's mapper renames Claude tool_input fields with a
	// `_redacted` suffix so the dashboard sanitizer renders them. Classifier
	// reads accept BOTH the renamed and bare keys so historical events stored
	// before the rename and gateway tests that POST events directly with bare
	// keys keep working.
	switch toolFold {
	case "bash":
		classifyBashCommand(inputStringAny(event.InputRedacted, "command_redacted", "command"), out)
	case "read":
		classifyFilePath(inputStringAny(event.InputRedacted, "file_path_redacted", "file_path", "path_redacted", "path"), false, out)
	case "edit", "write", "multiedit":
		classifyFilePath(inputStringAny(event.InputRedacted, "file_path_redacted", "file_path", "path_redacted", "path"), true, out)
	case "delete", "remove":
		classifyFileDelete(inputStringAny(event.InputRedacted, "file_path_redacted", "file_path", "path_redacted", "path"), out)
	case "move", "rename":
		classifyFileMove(
			inputStringAny(event.InputRedacted, "file_path_redacted", "file_path", "path_redacted", "path", "source_redacted", "source", "old_path_redacted", "old_path", "from_redacted", "from"),
			inputStringAny(event.InputRedacted, "destination_redacted", "destination", "dest_redacted", "dest", "dest_path", "target", "new_path_redacted", "new_path", "to_redacted", "to"),
			out,
		)
	default:
		out.ActionName = actionUnknownHook
		out.Capability = capabilityUnknown
		out.RiskTags = []string{"review_required", "unknown"}
		if looksHighImpact(event.InputRedacted) {
			out.RiskTags = append(out.RiskTags, "destructive")
			out.Labels["unknown.impact"] = "high"
		}
	}
}

func classifyBashCommand(command string, out *ActionClassification) {
	out.ActionName = "bash.exec"
	out.Capability = capabilityShell
	out.RiskTags = []string{"exec"}
	out.Labels["command.class"] = "unknown"
	out.Labels["command.family"] = "unknown"

	folded := strings.ToLower(strings.TrimSpace(command))
	if folded == "" {
		out.RiskTags = append(out.RiskTags, "review_required", "unknown")
		return
	}
	hasNetwork := hasAnyToken(folded, []string{"curl", "wget", "nc ", "netcat", "telnet", "ssh "})
	// EDGE-008.6: destructive denylist remains for explicit policy rules
	// (e.g. claude-code.deny-destructive-shell), but it is no longer the gate
	// for safety. Even when this returns false, an unrecognized shape falls to
	// command.class=unknown + review_required below — fail closed.
	if isDestructiveShell(folded) {
		out.RiskTags = append(out.RiskTags, "destructive", "filesystem")
		if hasNetwork {
			out.RiskTags = append(out.RiskTags, "network")
		}
		out.Labels["command.class"] = "destructive"
		out.Labels["command.family"] = "filesystem_delete"
		return
	}
	if isGitPushCommand(folded) {
		out.RiskTags = []string{"deploy", "git", "network"}
		out.Labels["command.class"] = "deploy"
		out.Labels["command.family"] = "git_push"
		return
	}
	if hasNetwork {
		out.RiskTags = append(out.RiskTags, "network")
		out.Labels["command.class"] = "network"
		out.Labels["command.family"] = "network_egress"
		return
	}
	if isInstallCommand(folded) {
		out.RiskTags = append(out.RiskTags, "install", "network")
		out.Labels["command.class"] = "dependency_change"
		out.Labels["command.family"] = "install"
		return
	}
	// EDGE-008.6: explicit allowlist replaces the previous HasPrefix/Contains
	// safe-detection. safeShellShape rejects any shell metacharacter (composition,
	// substitution, redirection, fork-bomb syntax) before checking argv0+args
	// against the narrow set from PRD §7.14 + §11.3. This is the only path that
	// produces command.class=safe.
	if family, ok := safeShellShape(folded); ok {
		out.RiskTags = append(out.RiskTags, family)
		out.Labels["command.class"] = "safe"
		out.Labels["command.family"] = family
		return
	}
	out.RiskTags = append(out.RiskTags, "review_required", "unknown")
}

func classifyFilePath(path string, write bool, out *ActionClassification) {
	if write {
		out.ActionName = "file.write"
		out.Capability = capabilityFileWrite
		out.RiskTags = []string{"filesystem", "write"}
	} else {
		out.ActionName = "file.read"
		out.Capability = capabilityFileRead
		out.RiskTags = []string{"filesystem", "read"}
	}
	addPathLabels(path, out)
	if out.Labels["path.class"] == "secret" {
		out.RiskTags = append(out.RiskTags, "secrets")
	}
	if out.Labels["path.class"] == "source_code" {
		out.RiskTags = append(out.RiskTags, "source_code")
	}
}

func classifyFileDelete(path string, out *ActionClassification) {
	out.ActionName = "file.delete"
	out.Capability = capabilityFileDelete
	out.RiskTags = []string{"destructive", "filesystem", "write"}
	addPathLabels(path, out)
	// Promote secrets/source_code path tags into risk tags so policy.evaluate
	// sees the destructive-on-sensitive combo without having to re-derive
	// it from labels.
	switch out.Labels["path.class"] {
	case "secret":
		out.RiskTags = append(out.RiskTags, "secrets")
	case "source_code":
		out.RiskTags = append(out.RiskTags, "source_code")
	}
}

func classifyFileMove(sourcePath, destinationPath string, out *ActionClassification) {
	out.ActionName = "file.move"
	out.Capability = capabilityFileMove
	out.RiskTags = []string{"filesystem", "write"}
	sourceLabels := classifyPathLabels(sourcePath)
	destinationLabels := Labels{}
	if strings.TrimSpace(destinationPath) != "" {
		destinationLabels = classifyPathLabels(destinationPath)
	}
	mergePathLabels(out.Labels, sourceLabels)
	mergePathLabels(out.Labels, destinationLabels)
	if hasPathClass("secret", sourceLabels, destinationLabels) {
		out.RiskTags = append(out.RiskTags, "secrets")
	}
	if hasPathClass("source_code", sourceLabels, destinationLabels) {
		out.RiskTags = append(out.RiskTags, "source_code")
	}
}

func classifyMCPEvent(event AgentActionEvent, out *ActionClassification) {
	server := inputStringAny(event.InputRedacted, "mcp_server", "server")
	tool := firstNonEmpty(inputStringAny(event.InputRedacted, "mcp_tool", "tool"), event.ToolName)
	action := inputStringAny(event.InputRedacted, "mcp_action", "action")
	if server != "" {
		out.Labels["mcp.server"] = safeLabelValue(server, "unknown")
	}
	if tool != "" {
		out.Labels["mcp.tool"] = safeLabelValue(tool, "unknown")
	}
	if action != "" {
		out.Labels["mcp.action"] = safeLabelValue(action, "unknown")
	}
	actionNamePart := safeLabelValue(tool, "tool")
	if actionNamePart == "tool" && action != "" {
		actionNamePart = safeLabelValue(action, "action")
	}
	out.ActionName = "mcp." + actionNamePart
	if isMutatingMCP(action, tool) {
		out.Capability = capabilityMCPMutate
		out.RiskTags = []string{"mcp", "mutating", "write"}
		return
	}
	out.Capability = capabilityMCPRead
	out.RiskTags = []string{"mcp", "read"}
}

func classifyLLMEvent(event AgentActionEvent, out *ActionClassification) {
	provider := firstNonEmpty(inputStringAny(event.InputRedacted, "provider", "llm_provider"), event.AgentProduct, event.Labels["llm.provider"])
	model := firstNonEmpty(inputStringAny(event.InputRedacted, "model", "llm_model"), event.Labels["llm.model"])
	if provider != "" {
		out.Labels["llm.provider"] = safeLabelValue(provider, "unknown")
	}
	if model != "" {
		out.Labels["llm.model"] = safeLabelValue(model, "unknown")
	}
	out.ActionName = "llm.request"
	out.Capability = capabilityLLMRequest
	out.RiskTags = []string{"llm", "provider_call"}
	if hasAnyInputKey(event.InputRedacted, "input", "prompt", "messages", "content", "data") {
		out.RiskTags = append(out.RiskTags, "data")
	}
	if hasAnyInputKey(event.InputRedacted, "cost", "cost_usd", "llm_cost_usd", "tokens", "input_tokens", "output_tokens") {
		out.RiskTags = append(out.RiskTags, "cost")
	}
}

func classifyRuntimeEvent(event AgentActionEvent, out *ActionClassification) {
	kind := strings.TrimSpace(string(event.Kind))
	switch kind {
	case string(EventKindRuntimeProcessExec):
		out.ActionName = "runtime.process.exec"
		out.Capability = capabilityRuntimeProcess
		out.RiskTags = []string{"exec", "runtime"}
		out.Labels["runtime.event"] = "process.exec"
		if process := inputStringAny(event.InputRedacted, "process", "command", "exe"); process != "" {
			out.Labels["runtime.process"] = safeLabelValue(process, "unknown")
		}
	case string(EventKindRuntimeFileRead), string(EventKindRuntimeFileWrite):
		runtimeEvent := strings.TrimPrefix(kind, "runtime.")
		out.ActionName = kind
		out.Capability = capabilityRuntimeFile
		out.RiskTags = []string{"filesystem", "runtime"}
		if runtimeEvent == "file.write" {
			out.RiskTags = append(out.RiskTags, "write")
		} else {
			out.RiskTags = append(out.RiskTags, "read")
		}
		out.Labels["runtime.event"] = runtimeEvent
		addPathLabels(inputStringAny(event.InputRedacted, "path", "file_path"), out)
		if out.Labels["path.class"] == "secret" {
			out.RiskTags = append(out.RiskTags, "secrets")
		}
		if out.Labels["path.class"] == "source_code" {
			out.RiskTags = append(out.RiskTags, "source_code")
		}
	case string(EventKindRuntimeNetworkConnect), string(EventKindRuntimeDNSQuery):
		runtimeEvent := strings.TrimPrefix(kind, "runtime.")
		out.ActionName = kind
		out.Capability = capabilityRuntimeNetwork
		out.RiskTags = []string{"network", "runtime"}
		out.Labels["runtime.event"] = runtimeEvent
		if host := inputStringAny(event.InputRedacted, "host", "hostname", "address"); host != "" {
			out.Labels["runtime.host"] = safeLabelValue(host, "unknown")
		}
	default:
		out.ActionName = "unknown.runtime"
		out.Capability = capabilityUnknown
		out.RiskTags = []string{"review_required", "runtime", "unknown"}
	}
}

func baseClassificationLabels(event AgentActionEvent) Labels {
	labels := Labels{}
	if event.Layer != "" {
		labels["edge.layer"] = string(event.Layer)
	}
	if event.Kind != "" {
		labels["edge.kind"] = string(event.Kind)
	}
	if product := strings.TrimSpace(event.AgentProduct); product != "" {
		labels["agent.product"] = safeLabelValue(product, "unknown")
	}
	if event.Layer == LayerHook {
		if event.Kind != "" {
			labels["hook.event"] = string(event.Kind)
		}
		if tool := strings.TrimSpace(event.ToolName); tool != "" {
			labels["hook.tool_name"] = safeLabelValue(tool, "unknown")
		}
	}
	return labels
}

func addPathLabels(path string, out *ActionClassification) {
	folded := normalizePathForClass(path)
	if folded == "" {
		out.Labels["path.class"] = "unknown"
		return
	}
	if strings.Contains(folded, "..") {
		out.Labels["path.traversal"] = "true"
	}
	if isSecretPath(folded) {
		out.Labels["path.class"] = "secret"
		return
	}
	if isSourceCodePath(folded) {
		out.Labels["path.class"] = "source_code"
		if strings.Contains(folded, "/auth/") || strings.Contains(folded, "auth") {
			out.Labels["path.sensitive_area"] = "auth"
		}
		return
	}
	out.Labels["path.class"] = "file"
}

func classifyPathLabels(path string) Labels {
	labels := Labels{}
	addPathLabels(path, &ActionClassification{Labels: labels})
	return labels
}

func mergePathLabels(dst Labels, src Labels) {
	if len(src) == 0 {
		return
	}
	dst["path.class"] = moreSensitivePathClass(dst["path.class"], src["path.class"])
	if src["path.traversal"] == "true" {
		dst["path.traversal"] = "true"
	}
	if src["path.sensitive_area"] != "" {
		dst["path.sensitive_area"] = src["path.sensitive_area"]
	}
}

func moreSensitivePathClass(left, right string) string {
	if pathClassRank(right) > pathClassRank(left) {
		return right
	}
	if left != "" {
		return left
	}
	return right
}

func pathClassRank(value string) int {
	switch value {
	case "secret":
		return 4
	case "source_code":
		return 3
	case "file":
		return 2
	case "unknown":
		return 1
	default:
		return 0
	}
}

func hasPathClass(value string, labelSets ...Labels) bool {
	for _, labels := range labelSets {
		if labels["path.class"] == value {
			return true
		}
	}
	return false
}

func classifiedInputContent(input map[string]any) ([]byte, string, int64, error) {
	if len(input) == 0 {
		return nil, "", 0, nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, "", 0, fmt.Errorf("input_redacted is invalid")
	}
	if len(payload) > MaxInputRedactedBytes {
		return nil, "", int64(len(payload)), fmt.Errorf("input_redacted exceeds max %d bytes", MaxInputRedactedBytes)
	}
	return payload, "application/json", int64(len(payload)), nil
}

func inputStringAny(input map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := inputString(input, key); value != "" {
			return value
		}
	}
	return ""
}

func inputString(input map[string]any, key string) string {
	if len(input) == 0 {
		return ""
	}
	value, ok := input[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func hasAnyInputKey(input map[string]any, keys ...string) bool {
	if len(input) == 0 {
		return false
	}
	for _, key := range keys {
		if _, ok := input[key]; ok {
			return true
		}
	}
	return false
}

func normalizePathForClass(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	path = filepath.ToSlash(path)
	return strings.ToLower(path)
}

func isSecretPath(path string) bool {
	padded := "/" + strings.TrimPrefix(path, "/")
	return matchesEnvSecretFile(padded) ||
		strings.Contains(padded, "/secrets/") ||
		strings.Contains(padded, "/.ssh/") ||
		strings.Contains(padded, "/.aws/") ||
		strings.Contains(padded, "/.kube/") ||
		strings.Contains(padded, "/.docker/") ||
		strings.Contains(padded, "/.config/gcloud/") ||
		strings.Contains(padded, "/.netrc") ||
		strings.Contains(padded, "/.npmrc") ||
		strings.Contains(padded, "/.pypirc") ||
		strings.Contains(padded, "/.dockercfg") ||
		strings.Contains(padded, "/.htpasswd") ||
		// /etc/passwd + sibling OS-credential paths (EDGE-064-FOLLOWUP, task-98ad858f).
		// Contains match (not HasPrefix) so nested fixtures like /tmp/etc/passwd
		// still classify as secret; the negative guard
		// TestIsSecretPathDoesNotFlagBenignPaths pins that hyphenated tokens like
		// /var/log/foo-etc-passwd.log don't false-positive.
		strings.Contains(padded, "/etc/passwd") ||
		strings.Contains(padded, "/etc/shadow") ||
		strings.Contains(padded, "/etc/sudoers") ||
		strings.Contains(padded, "/etc/gshadow") ||
		strings.Contains(padded, "/application_default_credentials") ||
		strings.Contains(path, "id_rsa") ||
		strings.Contains(path, "id_ed25519") ||
		strings.Contains(path, "id_ecdsa") ||
		strings.Contains(path, "credential") ||
		strings.Contains(path, "token") ||
		strings.Contains(path, "password") ||
		strings.Contains(path, "service-account") ||
		strings.Contains(path, "service_account") ||
		strings.HasSuffix(path, ".pem") ||
		strings.HasSuffix(path, ".key") ||
		strings.HasSuffix(path, ".crt") ||
		strings.HasSuffix(path, ".p12") ||
		strings.HasSuffix(path, ".pfx") ||
		strings.HasSuffix(path, ".kdbx")
}

// matchesEnvSecretFile narrows the original `/.env` substring match so
// `.env.example` and other clearly-non-secret template files do not
// false-positive. EDGE-064: the original `strings.Contains(path, "/.env")`
// matched any path containing `/.env` as a substring — including
// `.env.example`, `.env.template`, `.env.sample`. Real .env files
// (e.g. `/.env`, `/.env.local`, `/.env.production`) carry actual
// secrets; template/example files do NOT. This helper preserves
// matches on real .env variants while excluding well-known
// non-secret suffixes.
func matchesEnvSecretFile(padded string) bool {
	if !strings.Contains(padded, "/.env") {
		return false
	}
	for _, suffix := range []string{".example", ".sample", ".template", ".dist", ".defaults"} {
		if strings.HasSuffix(padded, "/.env"+suffix) {
			return false
		}
	}
	return true
}

func isSourceCodePath(path string) bool {
	return strings.Contains(path, "/src/") ||
		strings.HasSuffix(path, ".go") ||
		strings.HasSuffix(path, ".ts") ||
		strings.HasSuffix(path, ".tsx") ||
		strings.HasSuffix(path, ".js") ||
		strings.HasSuffix(path, ".jsx") ||
		strings.HasSuffix(path, ".py") ||
		strings.HasSuffix(path, ".java") ||
		strings.HasSuffix(path, ".kt")
}

func isDestructiveShell(command string) bool {
	return strings.Contains(command, "rm -rf") ||
		strings.Contains(command, "rm -fr") ||
		strings.Contains(command, "del /s") ||
		strings.Contains(command, "rmdir /s")
}

// isGitPushCommand reports whether a command invokes `git push`, including
// option-prefixed variants like `git -c http.proxy=foo push origin main` that
// the previous strict-positional check missed (senior review on PR #243).
// Leading git options (-c <key>=<val>, -C <path>, --git-dir=, --work-tree=, etc.)
// are skipped before checking the subcommand.
func isGitPushCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "git" {
		return false
	}
	i := 1
	for i < len(fields) {
		f := fields[i]
		// Two-token options that take a value as the next field.
		if f == "-c" || f == "-C" {
			i += 2
			continue
		}
		// Single-token options (long --foo and short -X variants).
		if strings.HasPrefix(f, "-") {
			i++
			continue
		}
		return f == "push"
	}
	return false
}

func isInstallCommand(command string) bool {
	return strings.HasPrefix(command, "npm install") ||
		strings.HasPrefix(command, "npm i ") ||
		strings.HasPrefix(command, "npm add ") ||
		strings.HasPrefix(command, "npm ci") ||
		strings.HasPrefix(command, "pnpm install") ||
		strings.HasPrefix(command, "pnpm add ") ||
		strings.HasPrefix(command, "yarn install") ||
		strings.HasPrefix(command, "yarn add ")
}

// shellMetaCharacters is the set of characters that compose, substitute, or
// redirect a shell command. Their presence in a command disqualifies it from
// the safe allowlist regardless of how the command begins. This is what makes
// `npm test && rm -rf /`, `npm test | mkfs.ext4 /dev/sda`, `git status \`rm -rf\“,
// and similar bypass cases impossible to silently allow as safe.
const shellMetaCharacters = ";&|<>`$(){}\n\r"

// safeShellShape implements the EDGE-008.6 explicit allowlist for benign shell
// commands. It returns the command family ("test", "build", "git_readonly")
// only when:
//
//  1. The command contains no shell metacharacters (no composition,
//     substitution, redirection, fork-bomb syntax), and
//  2. argv[0] and every argument match a narrow read-only/build-test set drawn
//     from PRD §7.14 (Demo policy defaults) and §11.3 (allow-test-commands rule).
//
// Anything else returns ("", false) and the caller must classify the command
// as `command.class=unknown` + `review_required` so policy can fail closed.
//
// The allowlist is intentionally narrow:
//   - npm/pnpm/yarn: only `<cmd> test [...args]`, `<cmd> run test [...args]`,
//     `<cmd> run build [...args]`. Install/audit/publish are NOT safe.
//   - go: only `go test [...]`, `go build [...]`. `go install`/`go get` are NOT safe.
//   - cargo: only `cargo test [...]`, `cargo build [...]`. `cargo publish` NOT safe.
//   - make: only `make build [...]` or `make test [...]` with optional `KEY=VAL`
//     variable assignments. Additional positional targets (e.g. `make build clean`)
//     and flags (`-f`, `-C`, etc.) disqualify the shape because Make targets are
//     user-controlled in the local Makefile and `-f`/`-C` can swap the Makefile.
//   - pytest, vitest: any args (no shell metas allowed by the gate above).
//   - git: only read-only subcommands (status/log/diff/show). `branch` and `tag`
//     are NOT safe — both can mutate state (`git branch -D foo`, `git tag -d foo`,
//     `git branch foo` create). Leading global options (-c, -C, --git-dir,
//     --work-tree) disqualify even read-only subcommands because they can swap
//     config or repository scope (`git -c core.fsmonitor=evil-binary status`).
//     Per-subcommand args are constrained to an explicit read-only flag allowlist;
//     unknown flags or write-capable flags (`--output`, `-o`, `--ext-diff`,
//     `--exec`, `-c`) are rejected to keep the allowlist strictly read-only.
func safeShellShape(command string) (string, bool) {
	folded := strings.TrimSpace(command)
	if folded == "" {
		return "", false
	}
	if strings.ContainsAny(folded, shellMetaCharacters) {
		return "", false
	}
	fields := strings.Fields(folded)
	if len(fields) == 0 {
		return "", false
	}
	switch fields[0] {
	case "npm", "pnpm", "yarn":
		if len(fields) >= 2 && fields[1] == "test" {
			return "test", true
		}
		if len(fields) >= 3 && fields[1] == "run" {
			switch fields[2] {
			case "test":
				return "test", true
			case "build":
				return "build", true
			}
		}
	case "go":
		if len(fields) >= 2 {
			switch fields[1] {
			case "test":
				return "test", true
			case "build":
				return "build", true
			}
		}
	case "pytest", "vitest":
		return "test", true
	case "cargo":
		if len(fields) >= 2 {
			switch fields[1] {
			case "test":
				return "test", true
			case "build":
				return "build", true
			}
		}
	case "make":
		if len(fields) >= 2 && safeMakeArgs(fields[2:]) {
			switch fields[1] {
			case "build":
				return "build", true
			case "test":
				return "test", true
			}
		}
	case "git":
		if len(fields) >= 2 && safeGitReadonlyArgs(fields[1], fields[2:]) {
			return "git_readonly", true
		}
	}
	return "", false
}

// safeMakeArgs allows only `KEY=VAL` variable assignments after `make build|test`.
// Any flag (`-f`, `-C`, `--no-print-directory`) or extra positional target like
// `clean`, `install_evil` is rejected. POSIX make variable assignments are
// `<NAME>=<VALUE>` where NAME starts with a letter or underscore and is
// followed by alphanumerics or underscore.
func safeMakeArgs(args []string) bool {
	for _, arg := range args {
		if !looksLikeMakeVarAssignment(arg) {
			return false
		}
	}
	return true
}

func looksLikeMakeVarAssignment(s string) bool {
	if s == "" {
		return false
	}
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	name := s[:eq]
	for i, r := range name {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// safeGitReadonlyArgs validates a git readonly invocation: subcommand must be
// status/log/diff/show, and every flag must match a small read-only allowlist
// per subcommand. Unknown flags are rejected so write-capable forms like
// `--output=FILE`, `-o FILE`, `--ext-diff`, `--exec=...`, `-c key=val` cannot
// silently classify as safe.
func safeGitReadonlyArgs(subcommand string, args []string) bool {
	allowed, ok := safeGitReadonlyFlags[subcommand]
	if !ok {
		return false
	}
	for _, arg := range args {
		if arg == "" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			// Positional pathspecs and revision refs are accepted; any shell
			// metacharacter is already blocked at the top of safeShellShape.
			continue
		}
		// Short numeric context like -U5 or -C50 (rename detection threshold)
		// is permitted on diff/show without naming each variant. The command
		// has been lowercased by classifyBashCommand before it reaches here,
		// so prefixes must be lowercase.
		if subcommand == "diff" || subcommand == "show" {
			if isShortNumericFlag(arg, "-u", "-b", "-m", "-c") {
				continue
			}
			if isLongValueFlag(arg, "--unified=", "--break-rewrites=", "--find-renames=", "--find-copies=") {
				continue
			}
		}
		if subcommand == "log" {
			// `-<N>` is git log shorthand for --max-count=N (e.g. `git log -10`).
			if len(arg) >= 2 && arg[0] == '-' && isAllDigits(arg[1:]) {
				continue
			}
			if isShortNumericFlag(arg, "-n") {
				continue
			}
			if isLongValueFlag(arg, "--max-count=", "--skip=", "--since=", "--until=", "--author=", "--committer=", "--grep=") {
				continue
			}
		}
		if _, allowedFlag := allowed[arg]; allowedFlag {
			continue
		}
		return false
	}
	return true
}

// safeGitReadonlyFlags is the per-subcommand allowlist of inert read-only
// flags. Anything not in this set (notably `--output`, `-o`, `--ext-diff`,
// `--exec`, `-c`, `-O`, format-specifying writers like `--output-indicator-*`)
// is rejected by safeGitReadonlyArgs so a future option that exposes a
// write/exec sink does not silently get classified as safe.
var safeGitReadonlyFlags = map[string]map[string]struct{}{
	"status": {
		"-s": {}, "--short": {},
		"-b": {}, "--branch": {},
		"--porcelain": {}, "--porcelain=v1": {}, "--porcelain=v2": {},
		"--long":            {},
		"--show-stash":      {},
		"--ahead-behind":    {},
		"--no-ahead-behind": {},
		"--untracked-files": {}, "--untracked-files=all": {}, "--untracked-files=normal": {}, "--untracked-files=no": {},
		"--ignored":    {},
		"--no-renames": {},
		"--renames":    {},
		"-z":           {},
		"--":           {},
	},
	"log": {
		"--oneline":       {},
		"--graph":         {},
		"--all":           {},
		"--decorate":      {},
		"--no-decorate":   {},
		"--abbrev-commit": {},
		"--no-color":      {},
		"--name-only":     {},
		"--name-status":   {},
		"--stat":          {},
		"--shortstat":     {},
		"--summary":       {},
		"--reverse":       {},
		"--first-parent":  {},
		"--merges":        {},
		"--no-merges":     {},
		"--":              {},
	},
	"diff": {
		"--staged":              {},
		"--cached":              {},
		"--name-only":           {},
		"--name-status":         {},
		"--stat":                {},
		"--shortstat":           {},
		"--summary":             {},
		"--no-color":            {},
		"-w":                    {},
		"-b":                    {},
		"--ignore-all-space":    {},
		"--ignore-space-change": {},
		"--ignore-blank-lines":  {},
		"--check":               {},
		"--exit-code":           {},
		"--quiet":               {},
		"--no-renames":          {},
		"--minimal":             {},
		"--patience":            {},
		"--histogram":           {},
		"--":                    {},
	},
	"show": {
		"--stat":        {},
		"--shortstat":   {},
		"--summary":     {},
		"--name-only":   {},
		"--name-status": {},
		"--no-color":    {},
		"-s":            {},
		"--no-patch":    {},
		"--":            {},
	},
}

// isShortNumericFlag reports whether arg is one of the prefixes followed by an
// optional numeric value (e.g. `-U5`, `-C50`). Bare prefix without the digit
// is also accepted because git treats `-U` followed by a separate token as
// the value, but we already require the value-token to be a positional
// (handled by the per-arg loop in safeGitReadonlyArgs).
func isShortNumericFlag(arg string, prefixes ...string) bool {
	for _, p := range prefixes {
		if !strings.HasPrefix(arg, p) {
			continue
		}
		rest := arg[len(p):]
		if rest == "" {
			return true
		}
		for _, r := range rest {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// isLongValueFlag reports whether arg matches one of the `--name=...` value
// flags. Empty value is allowed; the value content is not introspected because
// the shell-meta gate already rejects metacharacter values.
func isLongValueFlag(arg string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(arg, p) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasAnyToken(value string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func isMutatingMCP(action, tool string) bool {
	value := strings.ToLower(action + " " + tool)
	return hasAnyToken(value, []string{"create", "update", "delete", "write", "send", "post", "publish", "mutate", "merge"})
}

func looksHighImpact(input map[string]any) bool {
	joined := strings.ToLower(flattenInputStrings(input))
	return strings.Contains(joined, "delete") ||
		strings.Contains(joined, "drop") ||
		strings.Contains(joined, "production") ||
		strings.Contains(joined, "database")
}

func flattenInputStrings(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	parts := make([]string, 0, len(input))
	for key, value := range input {
		parts = append(parts, key, fmt.Sprint(value))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func safeLabelValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if !utf8.ValidString(value) {
		return "<redacted:invalid_utf8>"
	}
	if _, ok := secretStringType(value); ok {
		return defaultRedactionMarker
	}
	if len(value) <= MaxLabelValueBytes {
		return value
	}
	limit := MaxLabelValueBytes
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	if limit == 0 {
		return fallback
	}
	return value[:limit]
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneLabels(labels Labels) Labels {
	if len(labels) == 0 {
		return Labels{}
	}
	out := make(Labels, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}
