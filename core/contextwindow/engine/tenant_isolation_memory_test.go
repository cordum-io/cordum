package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const isolationMemoryID = "tenant-b/isolation-memory"
const isolationMemoryMarker = "PR103H-TENANT-B-PRIVATE"

type isolationMemorySnapshot struct {
	events  []string
	summary string
	index   []string
	chunk   string
}

func readIsolationMemory(t *testing.T, svc *Service) isolationMemorySnapshot {
	t.Helper()
	ctx := context.Background()
	events, err := svc.redis.LRange(ctx, svc.historyKey(isolationMemoryID), 0, -1).Result()
	if err != nil {
		t.Fatalf("read seeded events: %v, %v", events, err)
	}
	summary, err := svc.redis.Get(ctx, svc.summaryKey(isolationMemoryID)).Result()
	if err != nil || summary != isolationMemoryMarker+"-SUMMARY" {
		t.Fatalf("read seeded summary: %q, %v", summary, err)
	}
	index, err := svc.redis.SMembers(ctx, svc.chunkIndexKey(isolationMemoryID)).Result()
	if err != nil || len(index) != 1 || index[0] != "mem:"+isolationMemoryID+":chunk:0" {
		t.Fatalf("read seeded chunk index: %v, %v", index, err)
	}
	chunk, err := svc.redis.Get(ctx, index[0]).Result()
	if err != nil || !strings.Contains(chunk, isolationMemoryMarker+"-CHUNK") {
		t.Fatalf("read seeded chunk: %q, %v", chunk, err)
	}
	return isolationMemorySnapshot{events: events, summary: summary, index: index, chunk: chunk}
}

func newIsolationMemory(t *testing.T) (*Service, isolationMemorySnapshot) {
	t.Helper()
	svc, srv := newTestService(t)
	t.Cleanup(srv.Close)
	ctx := context.Background()
	// Tenant B's own shared write is the positive control.
	_, err := svc.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		MemoryId: isolationMemoryID, TenantId: "tenant-b",
		Mode:          pb.ContextMode_CONTEXT_MODE_CHAT,
		WriteKind:     pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
		WriterAgentId: "tenant-b/writer", ProvenanceRef: "test-provenance", ProvenanceVerified: true,
		ModelResponse: []byte(isolationMemoryMarker + "-EVENT"),
	})
	if err != nil {
		t.Fatalf("seed tenant B shared write: %v", err)
	}
	pipe := svc.redis.Pipeline()
	chunkKey := "mem:" + isolationMemoryID + ":chunk:0"
	pipe.Set(ctx, svc.summaryKey(isolationMemoryID), isolationMemoryMarker+"-SUMMARY", 0)
	pipe.SAdd(ctx, svc.chunkIndexKey(isolationMemoryID), chunkKey)
	pipe.Set(ctx, chunkKey, `{"path":"/app/main.go","content":"`+isolationMemoryMarker+`-CHUNK"}`, 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed summary and chunk: %v", err)
	}
	before := readIsolationMemory(t, svc)
	if len(before.events) != 1 || !strings.Contains(before.events[0], isolationMemoryMarker+"-EVENT") {
		t.Fatalf("tenant B positive control did not persist: %v", before.events)
	}
	return svc, before
}

// The request names B's memory id with no tenant, as a caller that is not tenant B.
func TestTenantIsolationContextEngineBuildWindow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    pb.ContextMode
		payload string
		marker  string
	}{
		{"events", pb.ContextMode_CONTEXT_MODE_CHAT, `{"prompt":"next"}`, isolationMemoryMarker + "-EVENT"},
		{"summary", pb.ContextMode_CONTEXT_MODE_RAG, `{"prompt":"next"}`, isolationMemoryMarker + "-SUMMARY"},
		{"chunks", pb.ContextMode_CONTEXT_MODE_RAG, `{"file_path":"/app/main.go","prompt":"next"}`, isolationMemoryMarker + "-CHUNK"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, before := newIsolationMemory(t)
			resp, err := svc.BuildWindow(context.Background(), &pb.BuildWindowRequest{
				MemoryId: isolationMemoryID, Mode: tc.mode, LogicalPayload: []byte(tc.payload),
			})
			if after := readIsolationMemory(t, svc); !reflect.DeepEqual(before, after) {
				t.Error("tenant B memory changed during foreign read")
			}
			if err != nil {
				t.Fatalf("build window: %v", err)
			}
			for _, msg := range resp.GetMessages() {
				if strings.Contains(msg.GetContent(), tc.marker) {
					t.Error("foreign read returned tenant B memory")
				}
			}
		})
	}
}

func TestTenantIsolationContextEngineSharedWrite(t *testing.T) {
	for _, tenant := range []string{"tenant-a", ""} {
		t.Run("tenant="+tenant, func(t *testing.T) {
			svc, before := newIsolationMemory(t)
			_, err := svc.UpdateMemory(context.Background(), &pb.UpdateMemoryRequest{
				MemoryId: isolationMemoryID, TenantId: tenant,
				Mode:          pb.ContextMode_CONTEXT_MODE_CHAT,
				WriteKind:     pb.MemoryWriteKind_MEMORY_WRITE_KIND_SHARED_POLICY_STATE,
				WriterAgentId: "tenant-a/writer", ProvenanceRef: "test-provenance", ProvenanceVerified: true,
				ModelResponse: []byte("foreign write"),
			})
			if !errors.Is(err, ErrSharedWriteTenantMismatch) {
				t.Errorf("write error = %v, want tenant mismatch", err)
			}
			if after := readIsolationMemory(t, svc); !reflect.DeepEqual(before, after) {
				t.Error("tenant B memory changed during rejected write")
			}
		})
	}
}

func TestTenantIsolationContextEngineChatWrite(t *testing.T) {
	svc, before := newIsolationMemory(t)
	_, err := svc.UpdateMemory(context.Background(), &pb.UpdateMemoryRequest{
		MemoryId: isolationMemoryID, TenantId: "tenant-a",
		Mode: pb.ContextMode_CONTEXT_MODE_CHAT, WriteKind: pb.MemoryWriteKind_MEMORY_WRITE_KIND_CHAT,
		WriterAgentId: "tenant-a/writer", ModelResponse: []byte("foreign write"),
	})
	if err != nil && !errors.Is(err, ErrSharedWriteTenantMismatch) {
		t.Errorf("unexpected write error: %v", err)
	}
	if after := readIsolationMemory(t, svc); !reflect.DeepEqual(before, after) {
		t.Error("tenant B memory changed during foreign CHAT write")
	}
}
