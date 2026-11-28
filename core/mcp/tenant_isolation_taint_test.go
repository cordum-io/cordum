package mcp

import (
	"context"
	"testing"
	"time"
)

func TestTenantIsolationMCPTaintReadOnly(t *testing.T) {
	client, _ := newMiniRedisDedupeBackend(t)
	s := NewRedisTaintStore(client, time.Hour)
	ctx, want := context.Background(), sampleSessionTaint()
	if err := s.Taint(ctx, "tenant-b", "shared-session", want); err != nil {
		t.Fatal(err)
	}
	before, ok, err := s.GetTaint(ctx, "tenant-b", "shared-session")
	if err != nil || !ok {
		t.Fatalf("seed read: %v, %v", ok, err)
	}
	assertTaintEqual(t, before, want)
	got, ok, err := s.GetTaint(ctx, "tenant-a", "shared-session")
	if err != nil || ok || got != nil {
		t.Errorf("foreign taint: %v, %v, %v", got, ok, err)
	}
	after, ok, err := s.GetTaint(ctx, "tenant-b", "shared-session")
	if err != nil || !ok {
		t.Fatalf("B taint readback: %v, %v", ok, err)
	}
	assertTaintEqual(t, after, *before)
}
