package shadow

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestTenantIsolationShadowFindingReadOnly(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateFinding(ctx, minimalCreateReq("tenant-b", "owner-b", "principal-b", "claude-code", FindingRiskHigh, "config_file", "tenant B marker"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetFinding(ctx, "tenant-b", created.FindingID)
	if err != nil || before == nil {
		t.Fatalf("seed read: %v", err)
	}
	got, err := s.GetFinding(ctx, "tenant-a", created.FindingID)
	if !errors.Is(err, ErrNotFound) || got != nil {
		t.Errorf("foreign finding: %v, %v", got, err)
	}
	after, err := s.GetFinding(ctx, "tenant-b", created.FindingID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B finding changed: %v", err)
	}
}

func TestTenantIsolationShadowExceptionReadOnly(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateException(ctx, minimalCreateExceptionReq("tenant-b"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetException(ctx, "tenant-b", created.ExceptionID)
	if err != nil || before == nil {
		t.Fatalf("seed read: %v", err)
	}
	got, err := s.GetException(ctx, "tenant-a", created.ExceptionID)
	if !errors.Is(err, ErrNotFound) || got != nil {
		t.Errorf("foreign exception: %v, %v", got, err)
	}
	after, err := s.GetException(ctx, "tenant-b", created.ExceptionID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B exception changed: %v", err)
	}
}
