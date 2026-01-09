package engine

import (
	"context"
	"encoding/json"
	"testing"

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
	chunkKey := svc.chunkKey("mem3", 0)
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
