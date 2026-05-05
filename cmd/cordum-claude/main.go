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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/cordum/cordum/core/edge/safeexec"
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
	bin, err := resolveCordumctlPath()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordum-claude: %s\n", err)
		return 1
	}
	full := append([]string{"edge", "claude"}, args...)
	cmd, err := safeexec.CommandContext(context.Background(), bin, full, safeexec.Options{
		Env:    os.Environ(),
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordum-claude: launch %s: %s\n", bin, err)
		return 1
	}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		_, _ = fmt.Fprintf(stderr, "cordum-claude: launch %s: %s\n", bin, err)
		return 1
	}
	return 0
}

// resolveCordumctlPath returns an absolute path to the cordumctl binary.
// Resolution order: (1) CORDUM_CLAUDE_CORDUMCTL_BIN env override, (2) sibling
// binary in the same directory as cord-claude itself, (3) PATH lookup. Always
// returns an absolute path so exec.Command does not trip Go 1.19+'s refusal to
// run executables resolved via the current directory.
func resolveCordumctlPath() (string, error) {
	if env := os.Getenv("CORDUM_CLAUDE_CORDUMCTL_BIN"); env != "" {
		abs, err := filepath.Abs(env)
		if err == nil {
			env = abs
		}
		if _, err := os.Stat(env); err != nil {
			return "", fmt.Errorf("cordumctl binary from CORDUM_CLAUDE_CORDUMCTL_BIN not usable: %w", err)
		}
		return env, nil
	}
	if self, err := os.Executable(); err == nil {
		dir := filepath.Dir(self)
		for _, name := range cordumctlCandidateNames() {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	if path, err := exec.LookPath(cordumctlBinName); err == nil {
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			return path, nil
		}
		return abs, nil
	}
	return "", fmt.Errorf("cordumctl binary not found: set CORDUM_CLAUDE_CORDUMCTL_BIN, place cordumctl beside cordum-claude, or add cordumctl to PATH")
}

func cordumctlCandidateNames() []string {
	if runtime.GOOS == "windows" {
		return []string{cordumctlBinName + ".exe", cordumctlBinName}
	}
	return []string{cordumctlBinName}
}
