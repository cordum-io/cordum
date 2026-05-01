package edge

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
// use this output when constructing policy inputs.
type ActionClassification struct {
	ActionName       string
	Capability       string
	RiskTags         []string
	Labels           Labels
	InputContent     []byte
	InputContentType string
	InputSizeBytes   int64
}

// ClassifyEvent normalizes an AgentActionEvent into deterministic policy
// dimensions. It does not mutate the input event and never stores raw command
// strings in labels.
func ClassifyEvent(event AgentActionEvent) (ActionClassification, error) {
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
	return classification, nil
}

func classifyHookEvent(event AgentActionEvent, out *ActionClassification) {
	toolFold := strings.ToLower(strings.TrimSpace(event.ToolName))
	switch toolFold {
	case "bash":
		classifyBashCommand(inputString(event.InputRedacted, "command"), out)
	case "read":
		classifyFilePath(inputStringAny(event.InputRedacted, "file_path", "path"), false, out)
	case "edit", "write", "multiedit":
		classifyFilePath(inputStringAny(event.InputRedacted, "file_path", "path"), true, out)
	case "delete", "remove":
		classifyFileDelete(inputStringAny(event.InputRedacted, "file_path", "path"), out)
	case "move", "rename":
		classifyFileMove(inputStringAny(event.InputRedacted, "file_path", "path", "source"), out)
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
	if isDestructiveShell(folded) {
		out.RiskTags = append(out.RiskTags, "destructive", "filesystem")
		if hasNetwork {
			out.RiskTags = append(out.RiskTags, "network")
		}
		out.Labels["command.class"] = "destructive"
		out.Labels["command.family"] = "filesystem_delete"
		return
	}
	if isGitPush(folded) {
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
	if isBuildCommand(folded) {
		out.RiskTags = append(out.RiskTags, "build")
		out.Labels["command.class"] = "safe"
		out.Labels["command.family"] = "build"
		return
	}
	if isTestCommand(folded) {
		out.RiskTags = append(out.RiskTags, "test")
		out.Labels["command.class"] = "safe"
		out.Labels["command.family"] = "test"
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
}

func classifyFileMove(path string, out *ActionClassification) {
	out.ActionName = "file.move"
	out.Capability = capabilityFileMove
	out.RiskTags = []string{"filesystem", "write"}
	addPathLabels(path, out)
}

func classifyMCPEvent(event AgentActionEvent, out *ActionClassification) {
	server := firstNonEmpty(inputStringAny(event.InputRedacted, "mcp_server", "server"), event.Labels["mcp.server"])
	tool := firstNonEmpty(inputStringAny(event.InputRedacted, "mcp_tool", "tool"), event.ToolName, event.Labels["mcp.tool"])
	action := firstNonEmpty(inputStringAny(event.InputRedacted, "mcp_action", "action"), event.Labels["mcp.action"])
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
		out.ActionName = strings.TrimPrefix(kind, "runtime.")
		out.Capability = capabilityRuntimeFile
		out.RiskTags = []string{"filesystem", "runtime"}
	case string(EventKindRuntimeNetworkConnect), string(EventKindRuntimeDNSQuery):
		out.ActionName = strings.TrimPrefix(kind, "runtime.")
		out.Capability = capabilityRuntimeNetwork
		out.RiskTags = []string{"network", "runtime"}
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
			labels["hook.tool_name"] = tool
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
	return strings.Contains(padded, "/.env") ||
		strings.Contains(padded, "/secrets/") ||
		strings.Contains(padded, "/.ssh/") ||
		strings.Contains(padded, "/.aws/") ||
		strings.Contains(path, "id_rsa") ||
		strings.Contains(path, "credential") ||
		strings.Contains(path, "token") ||
		strings.Contains(path, "password") ||
		strings.HasSuffix(path, ".pem") ||
		strings.HasSuffix(path, ".key") ||
		strings.HasSuffix(path, ".crt")
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

func isGitPush(command string) bool {
	fields := strings.Fields(command)
	return len(fields) >= 2 && fields[0] == "git" && fields[1] == "push"
}

func isBuildCommand(command string) bool {
	return strings.Contains(command, "npm run build") ||
		strings.HasPrefix(command, "go build") ||
		strings.Contains(command, " make build") ||
		strings.HasPrefix(command, "make build")
}

func isTestCommand(command string) bool {
	return strings.HasPrefix(command, "npm test") ||
		strings.Contains(command, "npm run test") ||
		strings.HasPrefix(command, "go test") ||
		strings.Contains(command, " go test ") ||
		strings.Contains(command, "pytest") ||
		strings.Contains(command, "vitest")
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
	if len(value) > MaxLabelValueBytes {
		return value[:MaxLabelValueBytes]
	}
	return value
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
