package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	defaultHTTPIncomingBuffer = 256
	defaultSSEEventBuffer     = 64
)

type httpSession struct {
	id     string
	events chan []byte
}

// HTTPTransport supports SSE + HTTP POST transport for MCP JSON-RPC messages.
type HTTPTransport struct {
	maxMessageBytes int
	responseTimeout time.Duration

	incoming chan *JSONRPCMessage
	done     chan struct{}
	closed   atomic.Bool

	mu       sync.RWMutex
	sessions map[string]*httpSession
	pending  map[string]chan *JSONRPCMessage
}

// ActiveSessionCount returns currently connected SSE clients.
func (t *HTTPTransport) ActiveSessionCount() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.sessions)
}

// IsClosed reports whether the transport has been closed.
func (t *HTTPTransport) IsClosed() bool {
	if t == nil {
		return true
	}
	return t.closed.Load()
}

// NewHTTPTransport constructs an HTTP transport with sensible defaults.
func NewHTTPTransport(maxMessageBytes int, responseTimeout time.Duration) *HTTPTransport {
	if maxMessageBytes <= 0 {
		maxMessageBytes = DefaultMaxMessageBytes
	}
	if responseTimeout <= 0 {
		responseTimeout = DefaultHTTPResponseTimeout
	}
	return &HTTPTransport{
		maxMessageBytes: maxMessageBytes,
		responseTimeout: responseTimeout,
		incoming:        make(chan *JSONRPCMessage, defaultHTTPIncomingBuffer),
		done:            make(chan struct{}),
		sessions:        make(map[string]*httpSession),
		pending:         make(map[string]chan *JSONRPCMessage),
	}
}

func (t *HTTPTransport) ReadMessage() (*JSONRPCMessage, error) {
	if t == nil {
		return nil, ErrTransportClosed
	}
	select {
	case <-t.done:
		return nil, ErrTransportClosed
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, ErrTransportClosed
		}
		return msg, nil
	}
}

func (t *HTTPTransport) WriteMessage(msg *JSONRPCMessage) error {
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

	if messageHasID(msg.ID) && msg.sessionID != "" {
		key := pendingKey(msg.sessionID, msg.ID)
		t.mu.Lock()
		replyCh, ok := t.pending[key]
		if ok {
			delete(t.pending, key)
		}
		t.mu.Unlock()
		if ok {
			select {
			case replyCh <- msg:
				return nil
			default:
				return nil
			}
		}
	}

	if msg.sessionID != "" {
		return t.writeSessionEvent(msg.sessionID, msg)
	}
	return nil
}

func (t *HTTPTransport) Close() error {
	if t == nil {
		return nil
	}
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(t.done)

	t.mu.Lock()
	for key, ch := range t.pending {
		delete(t.pending, key)
		close(ch)
	}
	for id, session := range t.sessions {
		delete(t.sessions, id)
		close(session.events)
	}
	t.mu.Unlock()

	close(t.incoming)
	return nil
}

// HandleSSE upgrades an HTTP connection into an SSE stream.
func (t *HTTPTransport) HandleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if t.closed.Load() {
		http.Error(w, "transport closed", http.StatusServiceUnavailable)
		return
	}

	session := &httpSession{
		id:     uuid.NewString(),
		events: make(chan []byte, defaultSSEEventBuffer),
	}
	t.mu.Lock()
	t.sessions[session.id] = session
	t.mu.Unlock()
	defer t.removeSession(session.id)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-MCP-Session-ID", session.id)
	w.WriteHeader(http.StatusOK)

	initial, _ := json.Marshal(map[string]string{"sessionId": session.id})
	_, _ = fmt.Fprintf(w, "event: session\ndata: %s\n\n", initial)
	flusher.Flush()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-r.Context().Done():
			return
		case event, ok := <-session.events:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
			flusher.Flush()
		case <-keepalive.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// HandleMessage accepts a single JSON-RPC message over HTTP POST.
// This doubles as streamable HTTP mode because request and response travel in one POST.
func (t *HTTPTransport) HandleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if t.closed.Load() {
		http.Error(w, "transport closed", http.StatusServiceUnavailable)
		return
	}

	body := http.MaxBytesReader(w, r.Body, int64(t.maxMessageBytes))
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			t.writeJSONRPCError(w, nil, -32600, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		t.writeJSONRPCError(w, nil, -32700, "parse error", http.StatusBadRequest)
		return
	}
	msg, err := decodeMessage(data)
	if err != nil {
		t.writeJSONRPCError(w, nil, -32700, "parse error", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(msg.Method) == "" && !messageHasID(msg.ID) {
		t.writeJSONRPCError(w, nil, -32600, "invalid request", http.StatusBadRequest)
		return
	}

	sessionID := sessionIDFromRequest(r)
	if sessionID == "" {
		sessionID = "direct-" + uuid.NewString()
	}
	msg.sessionID = sessionID

	var responseCh chan *JSONRPCMessage
	if messageHasID(msg.ID) {
		responseCh = make(chan *JSONRPCMessage, 1)
		key := pendingKey(sessionID, msg.ID)
		t.mu.Lock()
		t.pending[key] = responseCh
		t.mu.Unlock()
		defer t.removePending(key)
	}

	if err := t.enqueue(r.Context(), msg); err != nil {
		t.writeJSONRPCError(w, msg.ID, -32603, "internal error", http.StatusServiceUnavailable)
		return
	}

	if !messageHasID(msg.ID) {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	select {
	case <-t.done:
		t.writeJSONRPCError(w, msg.ID, -32603, "transport closed", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		t.writeJSONRPCError(w, msg.ID, -32603, "request canceled", http.StatusRequestTimeout)
		return
	case <-time.After(t.responseTimeout):
		t.writeJSONRPCError(w, msg.ID, -32603, "request timeout", http.StatusGatewayTimeout)
		return
	case resp, ok := <-responseCh:
		if !ok || resp == nil {
			t.writeJSONRPCError(w, msg.ID, -32603, "transport closed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "encode response", http.StatusInternalServerError)
		}
	}
}

func (t *HTTPTransport) enqueue(ctx context.Context, msg *JSONRPCMessage) error {
	if msg == nil {
		return fmt.Errorf("%w: message is nil", ErrInvalidMessage)
	}
	select {
	case <-t.done:
		return ErrTransportClosed
	case <-ctx.Done():
		return ctx.Err()
	case t.incoming <- msg:
		return nil
	}
}

func (t *HTTPTransport) removeSession(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	session, ok := t.sessions[sessionID]
	if !ok {
		return
	}
	delete(t.sessions, sessionID)
	close(session.events)
}

func (t *HTTPTransport) removePending(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, key)
}

func (t *HTTPTransport) writeSessionEvent(sessionID string, msg *JSONRPCMessage) error {
	t.mu.RLock()
	session, ok := t.sessions[sessionID]
	t.mu.RUnlock()
	if !ok || session == nil {
		return nil
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case <-t.done:
		return ErrTransportClosed
	case session.events <- data:
		return nil
	default:
		return errors.New("sse session buffer full")
	}
}

func (t *HTTPTransport) writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string, status int) {
	if status <= 0 {
		status = http.StatusBadRequest
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func sessionIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id := strings.TrimSpace(r.Header.Get("X-MCP-Session-ID")); id != "" {
		return id
	}
	if id := strings.TrimSpace(r.URL.Query().Get("session_id")); id != "" {
		return id
	}
	return ""
}

func pendingKey(sessionID string, id json.RawMessage) string {
	return sessionID + "|" + string(id)
}
