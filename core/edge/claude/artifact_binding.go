package claude

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Artifact binding ties an Edge approval to the inputs a Bash command names:
// the files on its command line, the git HEAD of its cwd and, for kubectl, the
// context and namespace it targets. The labels join the approval's action
// identity at the Gateway, and Run computes them again before it lets an
// allowed command start, so a change in between makes the claim fail. Files a
// script or tool opens on its own are not bound.
const (
	artifactMaxFiles     = 16
	artifactMaxFileBytes = 8 << 20
	// artifactShellSyntax makes the shell, not the command line, decide what
	// a command reads.
	artifactShellSyntax = "'\"\\;&|<>`$(){}*?[~\n"
)

var artifactInterpreters = []string{"sh", "bash", "zsh", "dash", "python", "python3", "node", "perl", "ruby"}

// artifactInputs is what the binding digest covers. Files maps each resolved
// path to the sha256 of its content; json.Marshal sorts the keys.
type artifactInputs struct {
	Files         map[string]string `json:"files,omitempty"`
	GitHead       string            `json:"git_head,omitempty"`
	KubeContext   string            `json:"kube_context,omitempty"`
	KubeNamespace string            `json:"kube_namespace,omitempty"`
}

// artifactBindingLabels returns the binding labels of a PreToolUse Bash call,
// and nil for any other hook call. family is the call's command.family label.
func artifactBindingLabels(ctx context.Context, input HookInput, env map[string]string, family string) map[string]string {
	if input.HookEventName != "PreToolUse" || !strings.EqualFold(input.ToolName, "Bash") {
		return nil
	}
	command, _ := input.ToolInput["command"].(string)
	inputs, reason := artifactCollect(command, input.CWD, env)
	if reason == "" && (family == "test" || family == "build") {
		// Test and build commands run code they do not name.
		reason = "implicit-inputs"
	}
	labels := map[string]string{"artifact.binding": "bound"}
	if reason != "" {
		labels["artifact.binding"] = "unbound"
		labels["artifact.unbound_reason"] = reason
	}
	if inputs == nil {
		return labels
	}
	if input.CWD != "" {
		inputs.GitHead = gitOutput(ctx, input.CWD, "rev-parse", "HEAD")
	}
	if inputs.KubeContext != "" {
		labels["kube.context"] = inputs.KubeContext
		labels["kube.namespace"] = inputs.KubeNamespace
	}
	if len(inputs.Files) > 0 || inputs.GitHead != "" || inputs.KubeContext != "" {
		labels["artifact.digest"] = artifactDigest(inputs, envValue(env, "CORDUM_AGENTD_HOOK_NONCE"))
	}
	return labels
}

// bindArtifacts adds the binding labels to req and returns them for
// recheckArtifacts.
func bindArtifacts(ctx context.Context, input HookInput, env map[string]string, req *AgentdRequest) map[string]string {
	labels := artifactBindingLabels(ctx, input, env, req.Labels["command.family"])
	if labels != nil {
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		maps.Copy(req.Labels, labels)
	}
	return labels
}

// recheckArtifacts denies an allowed call when its binding labels changed
// after req was built: the decision, and any approval behind it, was for the
// old inputs. agentd does not return the approval ref with an allow, so every
// allow is checked.
func recheckArtifacts(ctx context.Context, input HookInput, env map[string]string, req AgentdRequest, bound map[string]string, decision AgentdDecision) AgentdDecision {
	if bound == nil || decision.Decision != DecisionAllow ||
		maps.Equal(bound, artifactBindingLabels(ctx, input, env, req.Labels["command.family"])) {
		return decision
	}
	return AgentdDecision{
		Decision: DecisionDeny,
		Reason:   "Cordum Edge: a file, git HEAD or kube target this command names changed after it was evaluated; action was not run",
	}
}

// artifactCollect finds the inputs a command names, or the reason they cannot
// be determined.
func artifactCollect(command, cwd string, env map[string]string) (*artifactInputs, string) {
	if strings.ContainsAny(command, artifactShellSyntax) {
		return nil, "shell-syntax"
	}
	// Bash splits words on space and tab only (newline is rejected above), so
	// a field here is the word bash will see.
	fields := strings.FieldsFunc(command, func(r rune) bool { return r == ' ' || r == '\t' })
	for i, field := range fields {
		if slices.Contains(artifactInterpreters, filepath.Base(field)) &&
			(slices.Contains(fields[i+1:], "-c") || slices.Contains(fields[i+1:], "-e")) {
			return nil, "inline-code"
		}
	}
	inputs := &artifactInputs{Files: map[string]string{}}
	for _, field := range fields {
		names := []string{field}
		if _, value, ok := strings.Cut(field, "="); ok && value != "" {
			names = append(names, value)
		}
		for _, name := range names {
			if reason := inputs.addFile(cwd, name); reason != "" {
				return nil, reason
			}
		}
	}
	if slices.ContainsFunc(fields, artifactIsKubectl) {
		kubeContext, namespace := artifactKubeTarget(fields, cwd, env)
		if kubeContext == "" {
			return nil, "kube-context"
		}
		inputs.KubeContext, inputs.KubeNamespace = kubeContext, namespace
	}
	return inputs, ""
}

// addFile records name when it names a regular file. It returns the unbound
// reason when name is a URL, a directory or another non-regular file, cannot
// be read, or goes past the file limits.
func (in *artifactInputs) addFile(cwd, name string) string {
	if strings.Contains(name, "://") {
		return "url"
	}
	path := artifactPath(cwd, name)
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ""
	case err == nil && info.IsDir():
		return "directory"
	case err != nil || !info.Mode().IsRegular():
		return "unreadable"
	}
	if _, seen := in.Files[path]; seen {
		return ""
	}
	if len(in.Files) == artifactMaxFiles {
		return "limit"
	}
	file, reason := artifactOpen(path)
	if reason != "" {
		return reason
	}
	defer func() { _ = file.Close() }()
	sum := sha256.New()
	n, err := io.Copy(sum, io.LimitReader(file, artifactMaxFileBytes+1))
	if err != nil {
		return "unreadable"
	}
	if n > artifactMaxFileBytes {
		return "limit"
	}
	in.Files[path] = hex.EncodeToString(sum.Sum(nil))
	return ""
}

// artifactOpen opens a regular file of at most artifactMaxFileBytes. It stats
// before opening so it never opens a FIFO or a device, and checks the open
// file again.
func artifactOpen(path string) (*os.File, string) {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil, "unreadable"
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, "unreadable"
	}
	info, err := file.Stat()
	switch {
	case err != nil || !info.Mode().IsRegular():
		_ = file.Close()
		return nil, "unreadable"
	case info.Size() > artifactMaxFileBytes:
		_ = file.Close()
		return nil, "limit"
	}
	return file, ""
}

func artifactPath(cwd, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Join(cwd, name)
}

// artifactDigest is the keyed sha256 of the bound inputs, written as 8-char
// hex groups: agentd redacts long hex runs from labels, which would turn
// every digest into the same marker.
func artifactDigest(inputs *artifactInputs, key string) string {
	mac := sha256.New()
	if key != "" {
		mac = hmac.New(sha256.New, []byte(key))
	}
	encoded, _ := json.Marshal(inputs) // strings and a string map always marshal
	_, _ = mac.Write(encoded)
	sum := hex.EncodeToString(mac.Sum(nil))
	groups := make([]string, 0, len(sum)/8)
	for len(sum) > 0 {
		groups = append(groups, sum[:8])
		sum = sum[8:]
	}
	return strings.Join(groups, ".")
}
