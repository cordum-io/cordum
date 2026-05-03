// Command cordum-claude is a thin alias for `cordumctl edge claude`. It
// exists so users can drop a one-word wrapper in front of Claude Code
// without learning the cordumctl command tree. All argv after `cordum-claude`
// is forwarded verbatim — including the `--` boundary and post-`--` Claude
// args. There is no flag parsing here; configuration comes from
// `./cordum.yaml`, `~/.cordum/config.yaml`, env vars, and the same flags
// that `cordumctl edge claude` already accepts.
//
// HARD RAIL: this program does not bundle, fork, or version-lock Claude
// Code itself. It launches whatever `claude` binary is on PATH, exactly the
// same way `cordumctl edge claude` does, via the shared launcher in
// core/edge/claude/launcher.go. New Claude Code releases continue to work
// without changes here.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

const cordumctlBinName = "cordumctl"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	bin := os.Getenv("CORDUM_CLAUDE_CORDUMCTL_BIN")
	if bin == "" {
		bin = cordumctlBinName
	}
	full := append([]string{"edge", "claude"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "cordum-claude: launch %s: %s\n", bin, err)
		return 1
	}
	return 0
}
