package copilot

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestTenantIsolationCopilotSessionReadOnly(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	if err := s.AppendMessage(ctx, "tenant-b", "same-user", "session-b", CopilotMessage{Role: "tool", Content: "private B marker"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetSession(ctx, "tenant-b", "session-b", "same-user")
	if err != nil || before == nil || len(before.Messages) != 1 {
		t.Fatalf("seed read: %v, %v", before, err)
	}
	index, err := s.client.ZRange(ctx, sessionIndexKey("tenant-b", "same-user"), 0, -1).Result()
	if err != nil || len(index) != 1 || index[0] != "session-b" {
		t.Fatalf("seed index: %v, %v", index, err)
	}
	for _, tenant := range []string{"tenant-a", ""} {
		got, err := s.GetSession(ctx, tenant, "session-b", "same-user")
		if !errors.Is(err, ErrNotFound) || got != nil {
			t.Errorf("foreign session for %q: %v, %v", tenant, got, err)
		}
		items, err := s.client.ZRange(ctx, sessionIndexKey(tenant, "same-user"), 0, -1).Result()
		if err != nil || len(items) != 0 {
			t.Errorf("foreign index: %v, %v", items, err)
		}
	}
	after, err := s.GetSession(ctx, "tenant-b", "session-b", "same-user")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B session changed: %v", err)
	}
	afterIndex, err := s.client.ZRange(ctx, sessionIndexKey("tenant-b", "same-user"), 0, -1).Result()
	if err != nil || !reflect.DeepEqual(index, afterIndex) {
		t.Errorf("B index changed: %v", err)
	}
}
