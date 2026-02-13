package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultMaxMessageBytes bounds JSON-RPC payload size to limit memory usage.
	DefaultMaxMessageBytes = 1 << 20 // 1 MiB
	// DefaultHTTPResponseTimeout bounds sync HTTP request waiting for a server response.
	DefaultHTTPResponseTimeout = 30 * time.Second
)

var (
	ErrTransportClosed = errors.New("mcp transport closed")
	ErrInvalidMessage  = errors.New("mcp invalid message")
)

// Transport reads and writes JSON-RPC messages.
type Transport interface {
	ReadMessage() (*JSONRPCMessage, error)
	WriteMessage(msg *JSONRPCMessage) error
	Close() error
}

func decodeMessage(raw []byte) (*JSONRPCMessage, error) {
	msg := &JSONRPCMessage{}
	if err := json.Unmarshal(raw, msg); err != nil {
		return nil, fmt.Errorf("%w: decode json: %v", ErrInvalidMessage, err)
	}
	if strings.TrimSpace(msg.JSONRPC) == "" {
		msg.JSONRPC = JSONRPCVersion
	}
	if msg.JSONRPC != JSONRPCVersion {
		return nil, fmt.Errorf("%w: unsupported jsonrpc version %q", ErrInvalidMessage, msg.JSONRPC)
	}
	return msg, nil
}

func messageHasID(raw json.RawMessage) bool {
	return len(raw) > 0
}
