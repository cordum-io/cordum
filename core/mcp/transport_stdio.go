package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// StdioTransport reads/writes newline-delimited JSON-RPC messages over stdio.
type StdioTransport struct {
	scanner *bufio.Scanner
	out     *bufio.Writer
	logger  *log.Logger

	maxMessageBytes int
	closed          atomic.Bool
	mu              sync.Mutex

	inCloser  io.Closer
	outCloser io.Closer
}

// NewStdioTransport creates a stdio transport bound to process stdio.
func NewStdioTransport() *StdioTransport {
	return NewStdioTransportWithIO(os.Stdin, os.Stdout, os.Stderr, DefaultMaxMessageBytes)
}

// NewStdioTransportWithIO creates a stdio transport with explicit streams.
func NewStdioTransportWithIO(in io.Reader, out io.Writer, stderr io.Writer, maxMessageBytes int) *StdioTransport {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if maxMessageBytes <= 0 {
		maxMessageBytes = DefaultMaxMessageBytes
	}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)

	t := &StdioTransport{
		scanner:         scanner,
		out:             bufio.NewWriter(out),
		logger:          log.New(stderr, "mcp-stdio: ", log.LstdFlags|log.Lmsgprefix),
		maxMessageBytes: maxMessageBytes,
	}
	if c, ok := in.(io.Closer); ok {
		t.inCloser = c
	}
	if c, ok := out.(io.Closer); ok {
		t.outCloser = c
	}
	return t
}

func (t *StdioTransport) ReadMessage() (*JSONRPCMessage, error) {
	if t == nil {
		return nil, ErrTransportClosed
	}
	if t.closed.Load() {
		return nil, ErrTransportClosed
	}
	for {
		if ok := t.scanner.Scan(); !ok {
			if err := t.scanner.Err(); err != nil {
				if errors.Is(err, bufio.ErrTooLong) {
					return nil, fmt.Errorf("%w: message exceeds %d bytes", ErrInvalidMessage, t.maxMessageBytes)
				}
				return nil, err
			}
			if t.closed.Load() {
				return nil, ErrTransportClosed
			}
			return nil, io.EOF
		}
		line := strings.TrimSpace(t.scanner.Text())
		if line == "" {
			continue
		}
		msg, err := decodeMessage([]byte(line))
		if err != nil {
			t.logger.Printf("dropping invalid json-rpc input: %v", err)
			return nil, err
		}
		return msg, nil
	}
}

func (t *StdioTransport) WriteMessage(msg *JSONRPCMessage) error {
	if t == nil {
		return ErrTransportClosed
	}
	if msg == nil {
		return fmt.Errorf("%w: message is nil", ErrInvalidMessage)
	}
	if t.closed.Load() {
		return ErrTransportClosed
	}
	if strings.TrimSpace(msg.JSONRPC) == "" {
		msg.JSONRPC = JSONRPCVersion
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() {
		return ErrTransportClosed
	}
	if _, err := t.out.Write(data); err != nil {
		return err
	}
	if err := t.out.WriteByte('\n'); err != nil {
		return err
	}
	return t.out.Flush()
}

func (t *StdioTransport) Close() error {
	if t == nil {
		return nil
	}
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	var firstErr error
	if err := t.out.Flush(); err != nil {
		firstErr = err
	}
	if t.inCloser != nil {
		if err := t.inCloser.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if t.outCloser != nil {
		if err := t.outCloser.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
