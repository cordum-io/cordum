package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
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
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"hi"}`),
		ModelResponse:  []byte("ok"),
	})
	if err != nil {
		t.Fatalf("update memory: %v", err)
	}

	resp, err := svc.BuildWindow(context.Background(), &pb.BuildWindowRequest{
		MemoryId:       "mem1",
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
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"toolong"}`),
	})
	if err == nil {
		t.Fatalf("expected error for oversized user message")
	}
	if count, err := svc.redis.LLen(ctx, svc.historyKey("mem-limit")).Result(); err != nil || count != 0 {
		t.Fatalf("expected no history entries, got %d err=%v", count, err)
	}

	_, err = svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		MemoryId:       "mem-limit-2",
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"ok"}`),
		ModelResponse:  []byte("toolong"),
	})
	if err == nil {
		t.Fatalf("expected error for oversized assistant message")
	}
	if count, err := svc.redis.LLen(ctx, svc.historyKey("mem-limit-2")).Result(); err != nil || count != 0 {
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
		Mode:           pb.ContextMode_CONTEXT_MODE_CHAT,
		LogicalPayload: []byte(`{"prompt":"hi"}`),
		ModelResponse:  []byte("ok"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count, err := svc.redis.LLen(ctx, svc.historyKey("mem-ok")).Result(); err != nil || count != 2 {
		t.Fatalf("expected 2 history entries, got %d err=%v", count, err)
	}
}

func TestBuildWindowRAGSummary(t *testing.T) {
	svc, srv := newTestService(t)
	defer srv.Close()

	ctx := context.Background()
	if err := svc.redis.Set(ctx, svc.summaryKey("mem2"), "Summary", 0).Err(); err != nil {
		t.Fatalf("set summary: %v", err)
	}

	resp, err := svc.BuildWindow(ctx, &pb.BuildWindowRequest{
		MemoryId:       "mem2",
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
	idxKey := svc.chunkIndexKey("mem3")
	chunkKey := fmt.Sprintf("mem:%s:chunk:%d", "mem3", 0)
	if err := svc.redis.SAdd(ctx, idxKey, chunkKey).Err(); err != nil {
		t.Fatalf("set chunk index: %v", err)
	}
	if err := svc.redis.Set(ctx, chunkKey, payload, 0).Err(); err != nil {
		t.Fatalf("set chunk: %v", err)
	}

	resp, err := svc.BuildWindow(ctx, &pb.BuildWindowRequest{
		MemoryId:       "mem3",
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
	memID := "mem-chunk-limit"
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
	svc.redis.RPush(ctx, svc.historyKey("corrupt-mem"), "not-valid-json")

	// Install slog spy.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(orig) })

	// Call BuildWindow in CHAT mode — the corrupt entry should be skipped with a warning.
	resp, err := svc.BuildWindow(ctx, &pb.BuildWindowRequest{
		MemoryId:       "corrupt-mem",
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
