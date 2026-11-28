package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/model"
)

func scanMCPSeed(t *testing.T, sweep bool) (*MCPApprovalStore, []*MCPApprovalRecord) {
	t.Helper()
	a, b := miniredis.RunT(t), miniredis.RunT(t)
	client := testredis.NewTwoMasterClusterClient(t, a.Addr(), b.Addr())
	ctx := context.Background()
	var records []*MCPApprovalRecord
	for i := 0; i < 7; i++ {
		rec := &MCPApprovalRecord{ID: fmt.Sprintf("scan-%d", i), Tenant: "tenant", AgentID: "agent",
			ToolName: "tool", ArgsHash: "hash", Status: model.ApprovalStatusPending,
			ExpiresAt: time.Now().Add(time.Hour).UnixMicro()}
		if sweep && i < 6 {
			rec.ExpiresAt = time.Now().Add(-time.Hour).UnixMicro()
		} else if !sweep && i == 1 {
			rec.Status = model.ApprovalStatusApproved
		} else if !sweep && i == 4 {
			rec.Status = model.ApprovalStatusRejected
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, mcpApprovalKeyPrefix+rec.ID, raw, time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
		records = append(records, rec)
	}
	if err := client.SAdd(ctx, mcpApprovalIndexKeyPrefix+"scan", "not-a-record").Err(); err != nil {
		t.Fatal(err)
	}
	for _, node := range []*miniredis.Miniredis{a, b} {
		count := 0
		for _, rec := range records[:6] {
			if node.Exists(mcpApprovalKeyPrefix + rec.ID) {
				count++
			}
		}
		if count == 0 {
			t.Fatal("records must be stored on both masters (expired records in sweep mode)")
		}
	}
	return NewMCPApprovalStore(client), records
}

func TestMCPApprovalListByStatusTwoMasters(t *testing.T) {
	s, _ := scanMCPSeed(t, false)
	ctx := context.Background()
	var cursor uint64
	var ids []string
	for _, wantNext := range []uint64{3, 6, 0} {
		items, next, err := s.ListByStatus(ctx, " PENDING ", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > 2 || next != wantNext {
			t.Fatalf("page at %d: items=%d next=%d, want <=2 items and next=%d", cursor, len(items), next, wantNext)
		}
		for _, rec := range items {
			if rec.Status != model.ApprovalStatusPending {
				t.Fatalf("unexpected status: %+v", rec)
			}
			ids = append(ids, rec.ID)
		}
		cursor = next
	}
	if want := []string{"scan-0", "scan-2", "scan-3", "scan-5", "scan-6"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want each of %v exactly once", ids, want)
	}
	for _, cursor := range []uint64{7, ^uint64(0)} {
		items, next, err := s.ListByStatus(ctx, "pending", cursor, 2)
		if err != nil || len(items) != 0 || next != 0 {
			t.Fatalf("cursor %d: items=%v next=%d err=%v", cursor, items, next, err)
		}
	}
}

func TestMCPApprovalSweepExpiredTwoMasters(t *testing.T) {
	s, records := scanMCPSeed(t, true)
	ctx := context.Background()
	count, err := s.SweepExpired(ctx, time.Now())
	if err != nil || count != 6 {
		t.Fatalf("SweepExpired = %d, %v; want 6, nil", count, err)
	}
	for i, original := range records {
		rec, err := s.Get(ctx, original.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := model.ApprovalStatusExpired
		if i == 6 {
			want = model.ApprovalStatusPending
		}
		if rec.Status != want {
			t.Fatalf("%s status = %s, want %s", rec.ID, rec.Status, want)
		}
	}
}
