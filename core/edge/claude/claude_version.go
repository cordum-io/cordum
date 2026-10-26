package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge/safeexec"
)

// MinHookOnFailureVersion is the first Claude Code release that honors a
// command hook's "onFailure": "block". Older releases ignore the field, so a
// cordum-hook that cannot start, times out, or prints invalid JSON lets the
// action through even when the generated settings are fail-closed.
const MinHookOnFailureVersion = "2.1.295"

const claudeVersionProbeTimeout = 5 * time.Second

var claudeVersionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

// ClaudeCodeVersion runs `<claudePath> --version` and returns the first X.Y.Z
// it prints (Claude Code prints e.g. "2.1.295 (Claude Code)"). env follows
// safeexec semantics: nil means the sanitized current environment.
func ClaudeCodeVersion(ctx context.Context, claudePath string, env []string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, claudeVersionProbeTimeout)
	defer cancel()
	out, err := safeexec.RunCapture(runCtx, claudePath, []string{"--version"}, nil, safeexec.Options{
		Env:            env,
		MaxStdoutBytes: 4 << 10,
		MaxStderrBytes: 4 << 10,
	})
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", claudePath, err)
	}
	version := claudeVersionPattern.FindString(string(out.Stdout))
	if version == "" {
		return "", fmt.Errorf("%s --version printed no X.Y.Z version", claudePath)
	}
	return version, nil
}

// SupportsHookOnFailure reports whether a Claude Code X.Y.Z version is at
// least MinHookOnFailureVersion.
func SupportsHookOnFailure(version string) bool {
	got, want := versionParts(version), versionParts(MinHookOnFailureVersion)
	for i := range got {
		if got[i] != want[i] {
			return got[i] > want[i]
		}
	}
	return true
}

func versionParts(version string) [3]int {
	var parts [3]int
	for i, field := range strings.SplitN(version, ".", 3) {
		parts[i], _ = strconv.Atoi(field)
	}
	return parts
}

// HookOnFailureUnsupportedMessage explains what an old Claude Code version
// means for Cordum's fail-closed settings. Shared by the launcher preflight
// and `cordumctl edge doctor`.
func HookOnFailureUnsupportedMessage(version string) string {
	return fmt.Sprintf("Claude Code %s is older than %s and ignores the generated \"onFailure\": \"block\": "+
		"a cordum-hook that cannot start, times out, or prints invalid JSON fails open on this version", version, MinHookOnFailureVersion)
}

// preflightClaudeHookOnFailure checks the Claude Code binary the wrapper is
// about to launch. Fail-closed modes (every mode except observe) rely on
// "onFailure": "block"; an older Claude Code is refused in enterprise-strict
// and warned about otherwise. An undeterminable version is only reported, so a
// changed --version format cannot lock every user out.
func preflightClaudeHookOnFailure(ctx context.Context, claudePath, policyMode string, env []string, stderr io.Writer) error {
	if strings.EqualFold(strings.TrimSpace(policyMode), "observe") {
		return nil
	}
	version, err := ClaudeCodeVersion(ctx, claudePath, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordum edge claude: warning: could not determine the Claude Code version (%v); "+
			"Claude Code older than %s ignores \"onFailure\": \"block\", so hook failures would fail open\n", err, MinHookOnFailureVersion)
		return nil
	}
	if SupportsHookOnFailure(version) {
		return nil
	}
	msg := HookOnFailureUnsupportedMessage(version)
	if strings.EqualFold(strings.TrimSpace(policyMode), "enterprise-strict") {
		return errors.New(msg + "; enterprise-strict refuses to launch it, upgrade Claude Code to " + MinHookOnFailureVersion + " or later")
	}
	_, _ = fmt.Fprintf(stderr, "cordum edge claude: warning: %s; upgrade Claude Code to %s or later\n", msg, MinHookOnFailureVersion)
	return nil
}
