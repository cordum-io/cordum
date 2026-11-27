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
	"runtime"
	"slices"
	"strings"
	"time"
)

// Artifact binding ties an Edge approval to the inputs a Bash command names:
// the files on its command line, the git HEAD of its cwd and, for kubectl, the
// context and namespace it targets. The labels join the approval's action
// identity at the Gateway, and Run computes them again before it lets an
// allowed command start, so a change in between makes the claim fail. A call
// is bound only when the binding models its program and every argument
// (artifactRecognized); any other call is unbound and keeps its digest. Files
// a script or tool opens on its own are not bound.
const (
	artifactMaxFiles      = 16
	artifactMaxFileBytes  = 8 << 20
	artifactBindingBudget = time.Second
	// artifactPlainBytes are the only bytes a bound command holds. Any other
	// byte (a quote, a control character such as CR, a non-ASCII byte) lets
	// the shell, or a parser below it, read the command differently.
	artifactPlainBytes = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 \t-_./,:=@+"
)

// artifactNoCodeTools are the Claude Code tools that cannot run code. They get
// no binding labels; every other tool but Bash is unbound.
var artifactNoCodeTools = []string{"Read", "Write", "Edit", "MultiEdit", "NotebookEdit", "Glob", "Grep", "LS", "WebFetch", "WebSearch", "TodoWrite"}

// artifactInputs is what the binding digest covers. Files maps each resolved
// path to the sha256 of its content; json.Marshal sorts the keys.
type artifactInputs struct {
	Files         map[string]string `json:"files,omitempty"`
	GitHead       string            `json:"git_head,omitempty"`
	KubeContext   string            `json:"kube_context,omitempty"`
	KubeNamespace string            `json:"kube_namespace,omitempty"`
}

// artifactBindingLabels returns binding labels for PreToolUse calls. Only the
// Bash tool is modeled; every other tool is unbound, whatever its input holds,
// except artifactNoCodeTools, which get no labels. Other hooks get no labels.
// family is the call's command.family label.
func artifactBindingLabels(ctx context.Context, input HookInput, env map[string]string, family string) map[string]string {
	if input.HookEventName != "PreToolUse" || slices.Contains(artifactNoCodeTools, input.ToolName) {
		return nil
	}
	if input.ToolName != "Bash" {
		return map[string]string{"artifact.binding": "unbound", "artifact.unbound_reason": "unrecognized"}
	}
	command, _ := input.ToolInput["command"].(string)
	inputs, reason := artifactWithinBudget(ctx, func() (*artifactInputs, string) {
		return artifactCollect(command, input.CWD, env)
	})
	if inputs != nil && (family == "test" || family == "build") {
		// Test and build commands run code they do not name. This replaces
		// a soft reason; a hard one (no inputs) is kept.
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

// artifactWithinBudget runs collect for at most artifactBindingBudget. A file
// read cannot be cancelled and some never return (a FUSE file, /proc/kmsg read
// as root, a FIFO swapped in after the stat), so a collection that runs out of
// time leaves the call unbound instead of holding the hook past its deadline.
func artifactWithinBudget(ctx context.Context, collect func() (*artifactInputs, string)) (*artifactInputs, string) {
	ctx, cancel := context.WithTimeout(ctx, artifactBindingBudget)
	defer cancel()
	type result struct {
		inputs *artifactInputs
		reason string
	}
	done := make(chan result, 1)
	go func() {
		inputs, reason := collect()
		done <- result{inputs, reason}
	}()
	select {
	case r := <-done:
		return r.inputs, r.reason
	case <-ctx.Done():
		return nil, "timeout"
	}
}

// artifactCollect finds the inputs a command names. It returns nil and the
// reason when they cannot be determined, and the inputs with the
// artifactRecognized reason when they can.
func artifactCollect(command, cwd string, env map[string]string) (*artifactInputs, string) {
	if strings.Trim(command, artifactPlainBytes) != "" {
		return nil, "shell-syntax"
	}
	// Bash splits words on space and tab only, so a field here is the word
	// bash will see.
	fields := strings.FieldsFunc(command, func(r rune) bool { return r == ' ' || r == '\t' })
	if slices.ContainsFunc(fields, artifactZshEquals) {
		return nil, "shell-syntax"
	}
	inputs := &artifactInputs{Files: map[string]string{}}
	for _, field := range fields {
		for _, name := range artifactNames(field) {
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
	return inputs, artifactRecognized(fields, cwd, inputs)
}

// addFile records name when it names a regular file. It returns the unbound
// reason when name is a URL or a network path, a Windows name
// artifactWindowsUnreadable rejects, a directory or another non-regular file,
// a file Git Bash's MSYS programs (bash, sh, perl) open differently from Go,
// cannot be read at its stat size, or goes past the file limits. MSYS reads a
// SYSTEM file as a symlink and opens a name ending in '.' as written, where
// Windows strips the dot. A network path (//host/share, a share on Windows)
// is never stat'ed, so naming one opens no connection.
func (in *artifactInputs) addFile(cwd, name string) string {
	if name == "" {
		return ""
	}
	if strings.Contains(name, "://") || strings.HasPrefix(name, "//") {
		return "url"
	}
	path := artifactPath(cwd, name)
	if runtime.GOOS == "windows" && artifactWindowsUnreadable(name) || artifactSpecialPath(path) {
		return "unreadable"
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ""
	case err == nil && info.IsDir():
		return "directory"
	case err != nil || !info.Mode().IsRegular() || artifactSystemFile(info) ||
		runtime.GOOS == "windows" && artifactTrailingDot(name):
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
	if n != info.Size() {
		// A procfs or sysfs file, whose content depends on who reads it, or
		// a file being written.
		return "unreadable"
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
	file, err := os.Open(path) // #nosec G304 -- a file the command names, opened read-only to hash it; a lexical clean would resolve ".." differently from the command
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

// artifactPath joins name to cwd without a lexical clean: the kernel resolves
// ".." after a symlink to the link target's parent, as the command will.
func artifactPath(cwd, name string) string {
	if cwd == "" || filepath.IsAbs(name) || artifactNamespacePath(name) {
		return name
	}
	return cwd + string(filepath.Separator) + name
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
