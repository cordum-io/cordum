package network

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
)

// StdinIngestor reads log lines from an io.Reader (defaults to
// os.Stdin) line-by-line via bufio.Scanner. EOF terminates Stream
// cleanly (returns nil); ctx cancel terminates with ctx.Err().
type StdinIngestor struct {
	r io.Reader
}

// stdinScanBufferMax mirrors fileScanBufferMax — pathological long
// lines are bounded to 256 KiB.
const stdinScanBufferMax = 256 * 1024

// NewStdinIngestor wraps r as an ingestor. Pass nil to use os.Stdin.
// Returns *StdinIngestor (not LogIngestor) so callers can compose
// with type-specific assertions; the value satisfies LogIngestor.
func NewStdinIngestor(r io.Reader) *StdinIngestor {
	if r == nil {
		r = os.Stdin
	}
	return &StdinIngestor{r: r}
}

// SourceLabel satisfies LogIngestor.
func (i *StdinIngestor) SourceLabel() string { return "stdin" }

// Stream satisfies LogIngestor.
func (i *StdinIngestor) Stream(ctx context.Context, out chan<- LogRecord) error {
	if closer, ok := i.r.(io.Closer); ok {
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-ctx.Done():
				_ = closer.Close()
			case <-done:
			}
		}()
	}
	sc := bufio.NewScanner(i.r)
	sc.Buffer(make([]byte, 0, 64*1024), stdinScanBufferMax)
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
		return fmt.Errorf("network: scan stdin: %w", err)
	}
	return nil
}
