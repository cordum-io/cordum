package network

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// FileIngestor reads operator-supplied egress / proxy log files. The
// scanner drains the file line-by-line and emits one LogRecord per
// parseable line. EOF terminates Stream cleanly (returns nil); ctx
// cancel terminates with ctx.Err().
type FileIngestor struct {
	path string
}

// NewFileIngestor constructs a FileIngestor for the given path. The
// path is stat'd at construction so the caller learns about a
// non-existent / unreadable file before entering the ingest loop.
func NewFileIngestor(path string) (*FileIngestor, error) {
	if path == "" {
		return nil, errors.New("network: file path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("network: stat file %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("network: %q is a directory, expected a file", path)
	}
	return &FileIngestor{path: path}, nil
}

// SourceLabel satisfies LogIngestor.
func (i *FileIngestor) SourceLabel() string { return "file" }

// fileScanBufferMax caps the bufio.Scanner buffer so a single
// pathological line cannot exhaust memory. 256 KiB easily covers any
// well-formed egress / proxy log line.
const fileScanBufferMax = 256 * 1024

// Stream satisfies LogIngestor. Opens the file, drains it, and emits
// LogRecord values onto out. Closes the file on return.
func (i *FileIngestor) Stream(ctx context.Context, out chan<- LogRecord) error {
	f, err := os.Open(i.path)
	if err != nil {
		return fmt.Errorf("network: open %q: %w", i.path, err)
	}
	return streamFile(ctx, f, i.path, out)
}

func streamFile(ctx context.Context, f io.ReadCloser, label string, out chan<- LogRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	defer close(done)
	defer func() { _ = f.Close() }()
	go func() {
		select {
		case <-ctx.Done():
			_ = f.Close()
		case <-done:
		}
	}()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), fileScanBufferMax)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec, ok := parseRecord(sc.Text())
		if !ok {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rec:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("network: scan %q: %w", label, err)
	}
	return nil
}
