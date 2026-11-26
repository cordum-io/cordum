package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/tools/docscheck"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

type options struct {
	doc, root   string
	copies, env []string
	timeout     time.Duration
}

func run(args []string, stdout io.Writer) int {
	var o options
	fs := flag.NewFlagSet("docscheck-commands", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.StringVar(&o.doc, "doc", "docs/quickstart.md", "document to check")
	fs.StringVar(&o.root, "root", ".", "root for copied files")
	fs.DurationVar(&o.timeout, "timeout", 2*time.Minute, "timeout per block")
	fs.Func("copy", "file relative to root (repeatable)", func(s string) error { o.copies = append(o.copies, s); return nil })
	fs.Func("env", "environment name to pass (repeatable)", func(s string) error { o.env = append(o.env, s); return nil })
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || o.timeout <= 0 {
		// Already failing; a diagnostic write error cannot change the outcome.
		_, _ = fmt.Fprintln(stdout, "expected flags only and a positive timeout")
		return 1
	}
	blocks, err := docscheck.Parse(o.doc)
	if err == nil {
		var unmarked []docscheck.Line
		unmarked, err = docscheck.UnmarkedCommandBlocks(o.doc)
		for _, line := range unmarked {
			err = errors.Join(err, fmt.Errorf("%s:%d: shell block needs command or skip marker", o.doc, line.No))
		}
	}
	if err != nil {
		// Already failing, including when the diagnostic writer is unavailable.
		_, _ = fmt.Fprintf(stdout, "%v\ndocscheck-commands: checked=0 unchecked=0 failed=1\n", err)
		return 1
	}
	return checkBlocks(blocks, o, stdout)
}

func checkBlocks(blocks []docscheck.Block, o options, stdout io.Writer) int {
	checked, unchecked, failed := 0, 0, 0
	for _, b := range blocks {
		var message string
		if b.Has("skip") {
			unchecked++
			message = fmt.Sprintf("%s:%d: unchecked: %s\n", b.File, b.Line, b.Attrs["reason"])
		} else if b.Has("command") {
			checked++
			output, err := execute(b, o)
			if err != nil {
				failed++
				lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
				message = fmt.Sprintf("%s:%d: %v\n%s\n", b.File, b.Line, err, strings.Join(lines[max(0, len(lines)-20):], "\n"))
			} else {
				message = fmt.Sprintf("%s:%d: checked\n", b.File, b.Line)
			}
		}
		if _, err := io.WriteString(stdout, message); err != nil {
			return 1
		}
	}
	_, err := fmt.Fprintf(stdout, "docscheck-commands: checked=%d unchecked=%d failed=%d\n", checked, unchecked, failed)
	if err != nil || failed > 0 || checked == 0 {
		return 1
	}
	return 0
}

func execute(b docscheck.Block, o options) (output string, err error) {
	dir, err := os.MkdirTemp("", "docscheck-commands-")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	if err := copyFiles(o.root, dir, o.copies); err != nil {
		return "", err
	}
	var script strings.Builder
	for _, line := range b.Lines {
		script.WriteString(line.Text + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "block.sh"), []byte(script.String()), 0o600); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-e", "-o", "pipefail", "block.sh")
	cmd.Dir, cmd.Env = dir, environment(dir, o.env)
	cmd.WaitDelay = time.Second
	var buf boundedOutput
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	output = buf.String()
	if ctx.Err() != nil {
		return output, fmt.Errorf("timeout after %s: %w", o.timeout, ctx.Err())
	}
	if buf.full {
		return output, errors.New("command output exceeds 1 MiB")
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return output, err
	}
	return output, matchOutput(b, cmd.ProcessState.ExitCode(), output)
}

func matchOutput(b docscheck.Block, got int, output string) error {
	want, err := strconv.Atoi(b.Attrs["exit"])
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("exit code %d, expected %d", got, want)
	}
	for _, fragment := range b.Expect {
		if !strings.Contains(output, fragment) {
			return fmt.Errorf("missing output fragment %q", fragment)
		}
	}
	return nil
}

func environment(dir string, names []string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.ToSlash(dir)}
	for _, name := range names {
		// HOME always belongs to this block, even if explicitly allowlisted.
		if strings.EqualFold(name, "HOME") || strings.EqualFold(name, "PATH") || strings.ContainsAny(name, "=\x00") {
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func copyFiles(root, dir string, names []string) (err error) {
	if len(names) == 0 {
		return nil
	}
	source, err := os.OpenRoot(filepath.Clean(root))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	for _, name := range names {
		name = filepath.Clean(name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("copy path must be relative to root: %q", name)
		}
		data, err := source.ReadFile(name)
		if err != nil {
			return fmt.Errorf("copy %q: %w", name, err)
		}
		target := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Clean(target), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Keep diagnostics bounded even if a documentation command floods its output.
type boundedOutput struct {
	buf  bytes.Buffer
	full bool
}

func (b *boundedOutput) String() string { return b.buf.String() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := min(len(p), (1<<20)-b.buf.Len())
	b.full = b.full || n < len(p)
	if _, err := b.buf.Write(p[:n]); err != nil {
		return 0, err
	}
	return len(p), nil
}
