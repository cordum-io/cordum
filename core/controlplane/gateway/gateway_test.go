package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type stubMemStore struct {
	context map[string][]byte
	result  map[string][]byte
}

func (s *stubMemStore) PutContext(ctx context.Context, key string, data []byte) error {
	if s.context == nil {
		s.context = make(map[string][]byte)
	}
	s.context[key] = data
	return nil
}

func (s *stubMemStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	val, ok := s.context[key]
	if !ok {
		return nil, redis.Nil
	}
	return val, nil
}

func (s *stubMemStore) PutResult(ctx context.Context, key string, data []byte) error {
	if s.result == nil {
		s.result = make(map[string][]byte)
	}
	s.result[key] = data
	return nil
}

func (s *stubMemStore) GetResult(ctx context.Context, key string) ([]byte, error) {
	val, ok := s.result[key]
	if !ok {
		return nil, redis.Nil
	}
	return val, nil
}

func (s *stubMemStore) Close() error { return nil }

func TestNormalizeAPIKeyTrimsQuotes(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"super-secret-key":  "super-secret-key",
		"  super-secret  ":  "super-secret",
		"\"super-secret\"":  "super-secret",
		"'super-secret'":    "super-secret",
		" 'super-secret' ":  "super-secret",
		" \"super-secret\"": "super-secret",
	}
	for in, want := range cases {
		if got := normalizeAPIKey(in); got != want {
			t.Fatalf("normalizeAPIKey(%q)=%q want=%q", in, got, want)
		}
	}
}

func TestHandleStreamUpgradesWebsocketWithInstrumentation(t *testing.T) {
	s := &server{
		clients:  make(map[*websocket.Conn]chan *pb.BusPacket),
		eventsCh: make(chan *pb.BusPacket, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))
	srv := newIPv4Server(t, mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/stream"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	conn.Close()

	time.Sleep(20 * time.Millisecond)
}

func TestHandleStreamHonorsAPIKeySubprotocol(t *testing.T) {
	t.Setenv("API_KEY", "'super-secret-key'")
	provider, err := newBasicAuthProvider("default")
	if err != nil {
		t.Fatalf("auth init: %v", err)
	}

	s := &server{
		clients:  make(map[*websocket.Conn]chan *pb.BusPacket),
		eventsCh: make(chan *pb.BusPacket, 1),
		auth:     provider,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))
	srv := newIPv4Server(t, apiKeyMiddleware(provider, mux))
	defer srv.Close()

	okURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/stream"
	token := base64.RawURLEncoding.EncodeToString([]byte("super-secret-key"))
	dialer := websocket.Dialer{Subprotocols: []string{wsAPIKeyProtocol, token}}
	conn, _, err := dialer.Dial(okURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	_ = conn.Close()

	badURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/stream"
	_, resp, err := websocket.DefaultDialer.Dial(badURL, nil)
	if err == nil {
		t.Fatalf("expected dial error")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 response, got %#v err=%v", resp, err)
	}
}

func TestHandleGetMemoryFetchesContextByPointer(t *testing.T) {
	s := &server{
		memStore: &stubMemStore{
			context: map[string][]byte{
				"ctx:job-1": []byte(`{"prompt":"hi"}`),
			},
		},
	}

	req := httptest.NewRequest("GET", "/api/v1/memory?ptr="+url.QueryEscape("redis://ctx:job-1"), nil)
	rr := httptest.NewRecorder()
	s.handleGetMemory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp["kind"] != "context" {
		t.Fatalf("expected kind=context got=%v", resp["kind"])
	}
	if resp["key"] != "ctx:job-1" {
		t.Fatalf("expected key=ctx:job-1 got=%v", resp["key"])
	}
	jsonVal, ok := resp["json"].(map[string]any)
	if !ok {
		t.Fatalf("expected json object got=%T", resp["json"])
	}
	if jsonVal["prompt"] != "hi" {
		t.Fatalf("expected json.prompt=hi got=%v", jsonVal["prompt"])
	}
}

func TestApiKeyFromWebSocketProtocols(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil)
	token := base64.RawURLEncoding.EncodeToString([]byte("secret"))
	req.Header.Set("Sec-WebSocket-Protocol", wsAPIKeyProtocol+", "+token)
	if got := apiKeyFromWebSocket(req); got != "secret" {
		t.Fatalf("expected secret got %q", got)
	}
}

func TestApiKeyUnaryInterceptor(t *testing.T) {
	t.Setenv("CORDUM_API_KEYS", "secret")
	provider, err := newBasicAuthProvider("default")
	if err != nil {
		t.Fatalf("auth init: %v", err)
	}
	interceptor := apiKeyUnaryInterceptor(provider)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "secret"))
	_, err = interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "bad"))
	if _, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	}); err == nil {
		t.Fatalf("expected auth error")
	}
}

func TestHandleGetMemoryReturnsNotFoundForMissingKey(t *testing.T) {
	s := &server{memStore: &stubMemStore{}}

	req := httptest.NewRequest("GET", "/api/v1/memory?ptr="+url.QueryEscape("redis://res:missing"), nil)
	rr := httptest.NewRecorder()
	s.handleGetMemory(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got=%d body=%s", rr.Code, rr.Body.String())
	}
}

func newIPv4Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("skipping: unable to listen on ipv4 loopback (%v)", err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	return srv
}
