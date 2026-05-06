package safeexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
)

var ErrIOLimitExceeded = errors.New("safeexec: io limit exceeded")

const (
	DefaultMaxCaptureStdinBytes  int64 = 1 << 20
	DefaultMaxCaptureStdoutBytes int64 = 1 << 20
	DefaultMaxCaptureStderrBytes int64 = 256 << 10
)

type CaptureResult struct {
	Stdout []byte
	Stderr []byte
}

func RunCapture(ctx context.Context, argv0 string, args []string, stdin io.Reader, opts Options) (CaptureResult, error) {
	opts.MaxStdinBytes = captureLimit(opts.MaxStdinBytes, DefaultMaxCaptureStdinBytes)
	opts.MaxStdoutBytes = captureLimit(opts.MaxStdoutBytes, DefaultMaxCaptureStdoutBytes)
	opts.MaxStderrBytes = captureLimit(opts.MaxStderrBytes, DefaultMaxCaptureStderrBytes)
	input, err := readBoundedAll(stdin, opts.MaxStdinBytes, "stdin")
	if err != nil {
		return CaptureResult{}, err
	}
	var stdout, stderr bytes.Buffer
	stdoutWriter := &limitedWriter{dst: &stdout, remaining: opts.MaxStdoutBytes}
	stderrWriter := &limitedWriter{dst: &stderr, remaining: opts.MaxStderrBytes}
	cmdOpts := opts
	cmdOpts.Stdin = bytes.NewReader(input)
	cmdOpts.Stdout = stdoutWriter
	cmdOpts.Stderr = stderrWriter
	cmdOpts.MaxStdoutBytes = 0
	cmdOpts.MaxStderrBytes = 0
	cmd, err := CommandContext(ctx, argv0, args, cmdOpts)
	if err != nil {
		return CaptureResult{}, err
	}
	err = cmd.Run()
	result := CaptureResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if stdoutWriter.exceeded || stderrWriter.exceeded {
		return result, ErrIOLimitExceeded
	}
	return result, err
}

func captureLimit(configured, fallback int64) int64 {
	if configured > 0 {
		return configured
	}
	return fallback
}

func LimitWriter(w io.Writer, maxBytes int64) io.Writer {
	if w == nil || maxBytes <= 0 {
		return w
	}
	return &limitedWriter{dst: w, remaining: maxBytes}
}

type limitedWriter struct {
	dst       io.Writer
	remaining int64
	exceeded  bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		w.exceeded = true
		return 0, ErrIOLimitExceeded
	}
	if int64(len(p)) > w.remaining {
		n, _ := w.dst.Write(p[:w.remaining])
		w.remaining = 0
		w.exceeded = true
		return n, ErrIOLimitExceeded
	}
	n, err := w.dst.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func readBoundedAll(r io.Reader, maxBytes int64, stream string) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if maxBytes <= 0 {
		return io.ReadAll(r)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("safeexec: read %s: %w", stream, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrIOLimitExceeded
	}
	return data, nil
}
