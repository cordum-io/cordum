package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
	"github.com/cordum/cordum/core/infra/logging"
)

type cliOptions struct {
	Args     []string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Env      map[string]string
	Agentd   claude.AgentdClient
	Recorder edgecore.Recorder
}

func main() {
	logging.Init("cordum-hook")
	// Honor SIGINT/SIGTERM so a stalled agentd socket doesn't pin Claude Code
	// past its own deadline. The runner's own timeout still bounds the call;
	// signals just give the user/parent a clean way to abort sooner.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := runCLI(ctx, cliOptions{
		Args:   os.Args[1:],
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	os.Exit(code)
}

func runCLI(ctx context.Context, opts cliOptions) int {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if len(opts.Args) != 2 {
		writeUsage(opts.Stderr)
		return 2
	}
	switch opts.Args[0] {
	case "claude":
		return dispatchClaude(ctx, opts)
	case "copilot":
		return dispatchCopilot(ctx, opts)
	default:
		writeUsage(opts.Stderr)
		return 2
	}
}

func dispatchClaude(ctx context.Context, opts cliOptions) int {
	switch opts.Args[1] {
	case "pre-tool-use", "post-tool-use", "post-tool-use-failure", "user-prompt-submit", "config-change", "file-changed":
		return claude.Run(ctx, claude.RunOptions{
			Args:     opts.Args,
			Stdin:    opts.Stdin,
			Stdout:   opts.Stdout,
			Stderr:   opts.Stderr,
			Env:      opts.Env,
			Agentd:   opts.Agentd,
			Recorder: opts.Recorder,
		})
	default:
		writeUsage(opts.Stderr)
		return 2
	}
}

// dispatchCopilot governs GitHub Copilot (VS Code agent mode) hook events.
// Copilot's hook contract matches Claude Code's (same stdin payload fields and
// the same hookSpecificOutput.permissionDecision / exit-code response), so the
// governance events reuse claude.Run unchanged — only the product attribution
// differs (CORDUM_AGENT_PRODUCT=github-copilot, set below if not already in env
// so audit/sessions attribute the event to Copilot). Lifecycle-only events
// (SessionStart/Stop/PreCompact/Subagent*) are not gateable actions; they are
// recognized as no-ops (exit 0) so fail-closed never blocks a session from
// starting or ending.
func dispatchCopilot(ctx context.Context, opts cliOptions) int {
	switch opts.Args[1] {
	case "user-prompt-submit", "pre-tool-use", "post-tool-use":
		return claude.Run(ctx, claude.RunOptions{
			Args:     opts.Args,
			Stdin:    opts.Stdin,
			Stdout:   opts.Stdout,
			Stderr:   opts.Stderr,
			Env:      copilotEnv(opts.Env),
			Agentd:   opts.Agentd,
			Recorder: opts.Recorder,
		})
	case "session-start", "stop", "pre-compact", "subagent-start", "subagent-stop":
		// Lifecycle event — observe-only, never block. (Drain stdin so the
		// hook process doesn't leave Copilot's pipe writer blocked.)
		if opts.Stdin != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(opts.Stdin, 1<<20))
		}
		return 0
	default:
		writeUsage(opts.Stderr)
		return 2
	}
}

// copilotEnv returns the hook environment with CORDUM_AGENT_PRODUCT defaulted to
// github-copilot when the caller hasn't set it, so events are attributed to
// Copilot. All other environment (agentd URL, nonce, session id, fail-mode) is
// preserved. When opts.Env is nil, the runner reads os.Getenv; we seed a map
// from the process environment so the product default takes effect either way.
func copilotEnv(env map[string]string) map[string]string {
	out := map[string]string{}
	if env == nil {
		for _, kv := range os.Environ() {
			if i := indexByte(kv, '='); i >= 0 {
				out[kv[:i]] = kv[i+1:]
			}
		}
	} else {
		for k, v := range env {
			out[k] = v
		}
	}
	if out["CORDUM_AGENT_PRODUCT"] == "" {
		out["CORDUM_AGENT_PRODUCT"] = "github-copilot"
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func writeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `usage: cordum-hook <agent> <hook-event>

Supported Claude Code hook events:
  cordum-hook claude pre-tool-use
  cordum-hook claude post-tool-use
  cordum-hook claude post-tool-use-failure
  cordum-hook claude user-prompt-submit
  cordum-hook claude config-change
  cordum-hook claude file-changed

Supported GitHub Copilot (VS Code agent mode) hook events:
  cordum-hook copilot user-prompt-submit   # governs every chat
  cordum-hook copilot pre-tool-use         # governs every tool action (can block)
  cordum-hook copilot post-tool-use
  cordum-hook copilot session-start|stop|pre-compact|subagent-start|subagent-stop  # observe-only

The hook reads one agent hook JSON payload from stdin. Stdout is reserved for the agent-compatible JSON response; diagnostics go to stderr.
`)
}
