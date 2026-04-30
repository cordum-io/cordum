package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cordum/cordum/core/edge/claude"
	"github.com/cordum/cordum/core/infra/logging"
)

type cliOptions struct {
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Env    map[string]string
	Agentd claude.AgentdClient
}

func main() {
	logging.Init("cordum-hook")
	code := runCLI(context.Background(), cliOptions{
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
	if len(opts.Args) < 2 || opts.Args[0] != "claude" {
		writeUsage(opts.Stderr)
		return 2
	}
	switch opts.Args[1] {
	case "pre-tool-use", "post-tool-use", "post-tool-use-failure", "user-prompt-submit":
		return claude.Run(ctx, claude.RunOptions{
			Args:   opts.Args,
			Stdin:  opts.Stdin,
			Stdout: opts.Stdout,
			Stderr: opts.Stderr,
			Env:    opts.Env,
			Agentd: opts.Agentd,
		})
	default:
		writeUsage(opts.Stderr)
		return 2
	}
}

func writeUsage(w io.Writer) {
	fmt.Fprint(w, `usage: cordum-hook claude <hook-event>

Supported Claude hook events:
  cordum-hook claude pre-tool-use
  cordum-hook claude post-tool-use
  cordum-hook claude post-tool-use-failure
  cordum-hook claude user-prompt-submit

The hook reads one Claude hook JSON payload from stdin. Stdout is reserved for Claude-compatible JSON; diagnostics go to stderr.
`)
}
