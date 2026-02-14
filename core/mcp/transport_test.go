package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStdioTransportReadWrite(t *testing.T) {
	t.Parallel()
	inReader, inWriter := io.Pipe()
	defer inReader.Close()
	defer inWriter.Close()

	errBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}
	transport := NewStdioTransportWithIO(inReader, outBuf, errBuf, DefaultMaxMessageBytes)

	go func() {
		_, _ = io.WriteString(inWriter, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n")
	}()

	msg, err := transport.ReadMessage()
	if err != nil {
		t.Fatalf("read message failed: %v", err)
	}
	if msg.Method != MethodPing {
		t.Fatalf("expected method %q, got %q", MethodPing, msg.Method)
	}

	if err := transport.WriteMessage(&JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Result:  map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("write message failed: %v", err)
	}

	line := outBuf.String()
	if !strings.Contains(line, `"jsonrpc":"2.0"`) {
		t.Fatalf("expected jsonrpc version in output, got %q", line)
	}
}

func TestHTTPTransportMessageAndSSE(t *testing.T) {
	t.Parallel()
	transport := NewHTTPTransport(DefaultMaxMessageBytes, 2*time.Second)
	t.Cleanup(func() {
		_ = transport.Close()
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /mcp/sse", transport.HandleSSE)
	mux.HandleFunc("POST /mcp/message", transport.HandleMessage)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	go func() {
		msg, err := transport.ReadMessage()
		if err != nil || msg == nil {
			return
		}
		_ = transport.WriteMessage(&JSONRPCMessage{
			JSONRPC:   JSONRPCVersion,
			ID:        msg.ID,
			Result:    map[string]any{"pong": true},
			sessionID: msg.sessionID,
		})
	}()

	reqBody := strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"ping"}`)
	resp, err := http.Post(srv.URL+"/mcp/message", "application/json", reqBody)
	if err != nil {
		t.Fatalf("post message failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from message endpoint, got %d", resp.StatusCode)
	}
	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode message response failed: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", rpcResp.Error)
	}

	sseReq, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	if err != nil {
		t.Fatalf("new sse request failed: %v", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	sseResp, err := client.Do(sseReq)
	if err != nil {
		t.Fatalf("open sse failed: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from sse endpoint, got %d", sseResp.StatusCode)
	}
	if strings.TrimSpace(sseResp.Header.Get("X-MCP-Session-ID")) == "" {
		t.Fatal("expected X-MCP-Session-ID header")
	}
	br := bufio.NewReader(sseResp.Body)
	line1, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read sse line1 failed: %v", err)
	}
	line2, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read sse line2 failed: %v", err)
	}
	if !strings.HasPrefix(line1, "event: session") {
		t.Fatalf("expected session event line, got %q", line1)
	}
	if !strings.HasPrefix(line2, "data: ") {
		t.Fatalf("expected data line, got %q", line2)
	}
	if got := transport.ActiveSessionCount(); got < 1 {
		t.Fatalf("expected at least one active session, got %d", got)
	}
}
