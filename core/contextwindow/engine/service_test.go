package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func newTestService(t *testing.T) (*Service, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	svc, err := NewService("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.redis.Close()
	})
	return svc, srv
}

func TestBuildWindowRaw(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	resp, err := svc.BuildWindow(context.Background(), &pb.BuildWindowRequest{
		LogicalPayload: []byte(`{"prompt":"hello"}`),
		Mode:           pb.ContextMode_CONTEXT_MODE_RAW,
	})
	if err != nil {
		t.Fatalf("build window: %v", err)
	}
	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	if resp.Messages[0].GetRole() != "user" || resp.Messages[0].GetContent() != "hello" {
		t.Fatalf("unexpected message: %#v", resp.Messages[0])
	}
	if resp.OutputTokens != 1024 {
		t.Fatalf("expected default output tokens")
	}
}

func TestUpdateMemoryChat(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	_, err := svc.UpdateMemory(context.Background(), &pb.UpdateMemoryRequest{
		MemoryId:       "mem1",
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"hi"}`),
		ModelResponse:  []byte("ok"),
	})
	if err != nil {
		t.Fatalf("update memory: %v", err)
	}

	resp, err := svc.BuildWindow(context.Background(), &pb.BuildWindowRequest{
		MemoryId:       "mem1",
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"next"}`),
	})
	if err != nil {
		t.Fatalf("build window: %v", err)
	}
	if len(resp.Messages) < 3 {
		t.Fatalf("expected history + current messages, got %d", len(resp.Messages))
	}
	if resp.Messages[0].GetRole() != "user" || resp.Messages[1].GetRole() != "assistant" {
		t.Fatalf("unexpected history ordering")
	}
	if resp.Messages[len(resp.Messages)-1].GetContent() != "next" {
		t.Fatalf("expected last message to be current prompt")
	}
}

func TestUpdateMemoryRejectsOversize(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	svc.maxEntryBytes = 5
	ctx := context.Background()

	_, err := svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		MemoryId:       "mem-limit",
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"toolong"}`),
	})
	if err == nil {
		t.Fatalf("expected error for oversized user message")
	}
	if count, err := svc.redis.LLen(ctx, svc.historyKey("t1/mem-limit")).Result(); err != nil || count != 0 {
		t.Fatalf("expected no history entries, got %d err=%v", count, err)
	}

	_, err = svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		MemoryId:       "mem-limit-2",
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"ok"}`),
		ModelResponse:  []byte("toolong"),
	})
	if err == nil {
		t.Fatalf("expected error for oversized assistant message")
	}
	if count, err := svc.redis.LLen(ctx, svc.historyKey("t1/mem-limit-2")).Result(); err != nil || count != 0 {
		t.Fatalf("expected no history entries, got %d err=%v", count, err)
	}
}

func TestUpdateMemoryAcceptsWithinLimit(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	svc.maxEntryBytes = 100
	ctx := context.Background()

	_, err := svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		MemoryId:       "mem-ok",
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"hi"}`),
		ModelResponse:  []byte("ok"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count, err := svc.redis.LLen(ctx, svc.historyKey("t1/mem-ok")).Result(); err != nil || count != 2 {
		t.Fatalf("expected 2 history entries, got %d err=%v", count, err)
	}
}

func TestBuildWindowRAGSummary(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	ctx := context.Background()
	if err := svc.redis.Set(ctx, svc.summaryKey("t1/mem2"), "Summary", 0).Err(); err != nil {
		t.Fatalf("set summary: %v", err)
	}

	resp, err := svc.BuildWindow(ctx, &pb.BuildWindowRequest{
		MemoryId:       "t1/mem2",
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_RAG,
		LogicalPayload: []byte(`{"prompt":"hi"}`),
	})
	if err != nil {
		t.Fatalf("build window: %v", err)
	}
	if len(resp.Messages) < 2 {
		t.Fatalf("expected summary + user message")
	}
	if resp.Messages[0].GetRole() != "system" || resp.Messages[0].GetContent() != "Summary" {
		t.Fatalf("unexpected summary message: %#v", resp.Messages[0])
	}
}

func TestBuildWindowRAGFilePath(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	ctx := context.Background()
	rec := chunkRecord{Path: "/app/main.go", Content: "line 1"}
	payload, _ := json.Marshal(rec)
	idxKey := svc.chunkIndexKey("t1/mem3")
	chunkKey := fmt.Sprintf("mem:%s:chunk:%d", "t1/mem3", 0)
	if err := svc.redis.SAdd(ctx, idxKey, chunkKey).Err(); err != nil {
		t.Fatalf("set chunk index: %v", err)
	}
	if err := svc.redis.Set(ctx, chunkKey, payload, 0).Err(); err != nil {
		t.Fatalf("set chunk: %v", err)
	}

	resp, err := svc.BuildWindow(ctx, &pb.BuildWindowRequest{
		MemoryId:       "t1/mem3",
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_RAG,
		LogicalPayload: []byte(`{"file_path":"/app/main.go","prompt":"hi"}`),
	})
	if err != nil {
		t.Fatalf("build window: %v", err)
	}
	found := false
	for _, msg := range resp.Messages {
		if msg.GetRole() == "system" && msg.GetContent() != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected system context message")
	}
}

func TestBuildWindowRAGChunkLimit(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	svc.maxChunkScan = 1
	ctx := context.Background()
	memID := "t1/mem-chunk-limit"
	for i := 0; i < 3; i++ {
		rec := chunkRecord{Path: fmt.Sprintf("/app/file%d.go", i), Content: fmt.Sprintf("line %d", i)}
		payload, _ := json.Marshal(rec)
		chunkKey := fmt.Sprintf("mem:%s:chunk:%d", memID, i)
		if err := svc.redis.SAdd(ctx, svc.chunkIndexKey(memID), chunkKey).Err(); err != nil {
			t.Fatalf("set chunk index: %v", err)
		}
		if err := svc.redis.Set(ctx, chunkKey, payload, 0).Err(); err != nil {
			t.Fatalf("set chunk: %v", err)
		}
	}

	chunks := svc.loadChunks(ctx, memID)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk loaded, got %d", len(chunks))
	}

	resp, err := svc.BuildWindow(ctx, &pb.BuildWindowRequest{
		MemoryId:       memID,
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_RAG,
		LogicalPayload: []byte(`{"file_path":"/app","prompt":"hi"}`),
	})
	if err != nil {
		t.Fatalf("build window: %v", err)
	}
	count := 0
	for _, msg := range resp.Messages {
		if msg.GetRole() == "system" && strings.HasPrefix(msg.GetContent(), "Context from") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 context message, got %d", count)
	}
}

func TestExtractUserMessage(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	msg := svc.extractUserMessage([]byte(`{"instruction":"do","code_snippet":"x"}`))
	if msg == "" || msg == "do" {
		t.Fatalf("expected instruction with code")
	}
	msg = svc.extractUserMessage([]byte("plain"))
	if msg != "plain" {
		t.Fatalf("expected fallback string, got %s", msg)
	}
}

// NOTE: This test modifies the global slog default; do not add t.Parallel().
func TestBuildWindowCorruptHistoryLogsWarning(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	ctx := context.Background()
	// Push corrupt JSON into history.
	svc.redis.RPush(ctx, svc.historyKey("t1/corrupt-mem"), "not-valid-json")

	// Install slog spy.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(orig) })

	// Call BuildWindow in CHAT mode — the corrupt entry should be skipped with a warning.
	resp, err := svc.BuildWindow(ctx, &pb.BuildWindowRequest{
		MemoryId:       "t1/corrupt-mem",
		TenantId:       "t1",
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"hi"}`),
	})
	if err != nil {
		t.Fatalf("build window: %v", err)
	}
	// The corrupt entry should not appear in messages (only the user message).
	if len(resp.Messages) != 1 || resp.Messages[0].GetContent() != "hi" {
		t.Fatalf("expected only user message, got %d messages", len(resp.Messages))
	}
	// Assert warning was logged.
	logged := buf.String()
	if !strings.Contains(logged, "corrupt history event") {
		t.Fatalf("expected slog.Warn for corrupt history event, got: %s", logged)
	}
}

func TestTrimToBudget(t *testing.T) {
	msgs := []*pb.ModelMessage{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
	}
	trimmed := trimToBudget(msgs, 1)
	if len(trimmed) < 1 {
		t.Fatalf("expected at least one message")
	}
}

func TestRedisOpContextAddsDeadline(t *testing.T) {
	ctx := context.Background()
	redisCtx, cancel := redisOpContext(ctx)
	defer cancel()
	deadline, ok := redisCtx.Deadline()
	if !ok {
		t.Fatalf("expected redis context to have deadline")
	}
	if time.Until(deadline) <= 0 {
		t.Fatalf("expected deadline in the future")
	}
	if time.Until(deadline) > defaultRedisOpTimeout+time.Second {
		t.Fatalf("expected deadline within default timeout")
	}
}

func TestRedisOpContextPreservesDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	redisCtx, redisCancel := redisOpContext(ctx)
	defer redisCancel()

	orig, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("expected original deadline")
	}
	next, ok := redisCtx.Deadline()
	if !ok {
		t.Fatalf("expected redis context deadline")
	}
	if !orig.Equal(next) {
		t.Fatalf("expected deadline preserved, got %v want %v", next, orig)
	}
}

func TestTenantMemoryID(t *testing.T) {
	for _, tc := range []struct {
		tenant, id, want string
		ok               bool
	}{
		{"t1", "run:1", "t1/run:1", true},
		{" t1 ", "run:1", "t1/run:1", true},
		{"t1", "t1/run:1", "t1/run:1", true},
		{"t1", "t2/run:1", "", false},
		{"t1", "/run:1", "", false},
		{"t1", "t1/", "", false},
		{"t1", " ", "", false},
		{"", "run:1", "", false},
		{" \t", "run:1", "", false},
		{"t1/x", "run:1", "", false},
	} {
		got, ok := store.TenantMemoryID(tc.tenant, tc.id)
		if got != tc.want || ok != tc.ok {
			t.Errorf("TenantMemoryID(%q, %q) = %q, %v; want %q, %v", tc.tenant, tc.id, got, ok, tc.want, tc.ok)
		}
	}
}

var memoryWindowCases = []struct {
	name    string
	mode    pb.ContextMode
	payload string
	suffix  string
}{
	{"events", pb.ContextMode_CONTEXT_MODE_CHAT, `{"prompt":"next"}`, "-EVENT"},
	{"summary", pb.ContextMode_CONTEXT_MODE_RAG, `{"prompt":"next"}`, "-SUMMARY"},
	{"chunks", pb.ContextMode_CONTEXT_MODE_RAG, `{"file_path":"/app/main.go","prompt":"next"}`, "-CHUNK"},
}

func windowHas(t *testing.T, svc *Service, req *pb.BuildWindowRequest, marker string) bool {
	t.Helper()
	resp, err := svc.BuildWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("build window: %v", err)
	}
	for _, msg := range resp.GetMessages() {
		if strings.Contains(msg.GetContent(), marker) {
			return true
		}
	}
	return false
}

func TestTenantIsolationContextEngineOwnTenant(t *testing.T) {
	for _, tc := range memoryWindowCases {
		for _, reader := range []struct {
			tenant string
			want   bool
		}{{"tenant-b", true}, {"tenant-a", false}} {
			t.Run(tc.name+"/tenant="+reader.tenant, func(t *testing.T) {
				svc, before := newIsolationMemory(t)
				req := &pb.BuildWindowRequest{
					MemoryId: isolationMemoryID, TenantId: reader.tenant,
					Mode: tc.mode, LogicalPayload: []byte(tc.payload),
				}
				if got := windowHas(t, svc, req, isolationMemoryMarker+tc.suffix); got != reader.want {
					t.Errorf("window holds tenant B memory = %v, want %v", got, reader.want)
				}
				if after := readIsolationMemory(t, svc); !reflect.DeepEqual(before, after) {
					t.Error("tenant B memory changed during read")
				}
			})
		}
	}
}

func TestTenantIsolationContextEngineSharedMemoryID(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()
	ctx := context.Background()
	tenants := []string{"tenant-a", "tenant-b"}
	for _, tenant := range tenants {
		if _, err := svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
			MemoryId: "run:shared", TenantId: tenant,
			Mode: pb.ContextMode_CONTEXT_MODE_CHAT, ModelResponse: []byte("MARK-" + tenant),
		}); err != nil {
			t.Fatalf("%s write: %v", tenant, err)
		}
	}
	for i, tenant := range tenants {
		other := tenants[1-i]
		events, err := svc.redis.LRange(ctx, "mem:"+tenant+"/run:shared:events", 0, -1).Result()
		if err != nil || len(events) != 1 || !strings.Contains(events[0], "MARK-"+tenant) {
			t.Errorf("%s stored events = %v, %v", tenant, events, err)
		}
		req := &pb.BuildWindowRequest{
			MemoryId: "run:shared", TenantId: tenant,
			Mode: pb.ContextMode_CONTEXT_MODE_CHAT, LogicalPayload: []byte(`{"prompt":"next"}`),
		}
		if !windowHas(t, svc, req, "MARK-"+tenant) || windowHas(t, svc, req, "MARK-"+other) {
			t.Errorf("%s window must hold its own memory and not %s's", tenant, other)
		}
	}
	if n, err := svc.redis.Exists(ctx, svc.historyKey("run:shared")).Result(); err != nil || n != 0 {
		t.Errorf("unqualified history key exists: %d, %v", n, err)
	}
}

func legacyMemorySnapshot(t *testing.T, svc *Service) string {
	t.Helper()
	ctx := context.Background()
	events, err1 := svc.redis.LRange(ctx, svc.historyKey("run:legacy"), 0, -1).Result()
	summary, err2 := svc.redis.Get(ctx, svc.summaryKey("run:legacy")).Result()
	index, err3 := svc.redis.SMembers(ctx, svc.chunkIndexKey("run:legacy")).Result()
	chunk, err4 := svc.redis.Get(ctx, "mem:run:legacy:chunk:0").Result()
	if err := errors.Join(err1, err2, err3, err4); err != nil || len(events) != 1 || len(index) != 1 {
		t.Fatalf("read legacy memory: %v, %v, %v", events, index, err)
	}
	return fmt.Sprint(events, summary, index, chunk)
}

func TestTenantIsolationContextEngineLegacyMemory(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()
	ctx := context.Background()
	const marker = "LEGACY-UNQUALIFIED"
	pipe := svc.redis.Pipeline()
	pipe.RPush(ctx, svc.historyKey("run:legacy"), `{"role":"user","content":"`+marker+`-EVENT"}`)
	pipe.Set(ctx, svc.summaryKey("run:legacy"), marker+"-SUMMARY", 0)
	pipe.SAdd(ctx, svc.chunkIndexKey("run:legacy"), "mem:run:legacy:chunk:0")
	pipe.Set(ctx, "mem:run:legacy:chunk:0", `{"path":"/app/main.go","content":"`+marker+`-CHUNK"}`, 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed legacy memory: %v", err)
	}
	before := legacyMemorySnapshot(t, svc)
	for _, tc := range memoryWindowCases {
		for _, tenant := range []string{"tenant-a", "tenant-b", ""} {
			req := &pb.BuildWindowRequest{
				MemoryId: "run:legacy", TenantId: tenant, Mode: tc.mode, LogicalPayload: []byte(tc.payload),
			}
			if windowHas(t, svc, req, marker+tc.suffix) {
				t.Errorf("%s window for tenant %q holds unqualified memory", tc.name, tenant)
			}
		}
	}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if _, err := svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
			MemoryId: "run:legacy", TenantId: tenant,
			Mode: pb.ContextMode_CONTEXT_MODE_CHAT, ModelResponse: []byte("tenant write"),
		}); err != nil {
			t.Fatalf("%s write: %v", tenant, err)
		}
	}
	if after := legacyMemorySnapshot(t, svc); after != before {
		t.Errorf("unqualified memory changed: %s, want %s", after, before)
	}
}

func TestTenantIsolationContextEngineTenantlessWrite(t *testing.T) {
	for _, tc := range []struct {
		name, tenant string
		mode         pb.ContextMode
	}{
		{"chat/empty", "", pb.ContextMode_CONTEXT_MODE_CHAT},
		{"chat/blank", " \t", pb.ContextMode_CONTEXT_MODE_CHAT},
		{"rag/empty", "", pb.ContextMode_CONTEXT_MODE_RAG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, srv := newTestService(t)
			defer srv.Close()
			ctx := context.Background()
			_, err := svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
				MemoryId: "run:tenantless", TenantId: tc.tenant, Mode: tc.mode,
				LogicalPayload: []byte(`{"prompt":"hi"}`), ModelResponse: []byte("ok"),
			})
			if !errors.Is(err, ErrSharedWriteTenantMismatch) {
				t.Errorf("write error = %v, want tenant mismatch", err)
			}
			if keys, err := svc.redis.Keys(ctx, "*").Result(); err != nil || len(keys) != 0 {
				t.Errorf("refused write stored keys %v, %v", keys, err)
			}
		})
	}
}
