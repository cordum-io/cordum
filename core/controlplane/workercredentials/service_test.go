package workercredentials

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
)

func newTestService(t *testing.T) *Service {
	t.Helper()

	redisSrv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(redisSrv.Close)

	cfg, err := configsvc.New("redis://" + redisSrv.Addr())
	if err != nil {
		t.Fatalf("config service: %v", err)
	}
	t.Cleanup(func() { _ = cfg.Close() })

	return NewService(cfg)
}

func TestCreateAndVerifyCredential(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	createdAt := time.Date(2026, time.April, 7, 12, 30, 0, 0, time.UTC)

	issued, err := svc.Create(ctx, IssueInput{
		WorkerID:      " worker-a ",
		AllowedPools:  []string{"gpu", "default", "default", " "},
		AllowedTopics: []string{"job.beta", "job.alpha", "job.beta"},
		PackID:        " pack-a ",
		CreatedBy:     " tester ",
		CreatedAt:     createdAt,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issued.Token == "" {
		t.Fatal("expected plaintext token")
	}
	if issued.Credential.CredentialHash == "" || issued.Credential.CredentialHash == issued.Token {
		t.Fatalf("expected stored hash, got %+v", issued.Credential)
	}
	if issued.Credential.WorkerID != "worker-a" {
		t.Fatalf("expected trimmed worker id, got %q", issued.Credential.WorkerID)
	}
	if issued.Credential.PackID != "pack-a" {
		t.Fatalf("expected trimmed pack id, got %q", issued.Credential.PackID)
	}
	if issued.Credential.CreatedBy != "tester" {
		t.Fatalf("expected trimmed created_by, got %q", issued.Credential.CreatedBy)
	}
	if issued.Credential.CreatedAt != createdAt.Format(time.RFC3339) {
		t.Fatalf("expected created_at %q, got %q", createdAt.Format(time.RFC3339), issued.Credential.CreatedAt)
	}
	if got, want := issued.Credential.AllowedPools, []string{"default", "gpu"}; !equalStrings(got, want) {
		t.Fatalf("allowed pools mismatch: got %v want %v", got, want)
	}
	if got, want := issued.Credential.AllowedTopics, []string{"job.alpha", "job.beta"}; !equalStrings(got, want) {
		t.Fatalf("allowed topics mismatch: got %v want %v", got, want)
	}

	record, ok, err := svc.Verify(ctx, "worker-a", issued.Token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok || record == nil {
		t.Fatalf("expected successful verification, got ok=%v record=%v", ok, record)
	}
	if record.WorkerID != "worker-a" {
		t.Fatalf("expected verified record for worker-a, got %+v", record)
	}

	_, ok, err = svc.Verify(ctx, "worker-a", "wrong-token")
	if err != nil {
		t.Fatalf("Verify wrong token: %v", err)
	}
	if ok {
		t.Fatal("expected wrong token verification to fail")
	}
}

func TestRevokedCredentialFails(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	issued, err := svc.Create(ctx, IssueInput{
		WorkerID:  "worker-revoked",
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Revoke(ctx, issued.Credential.WorkerID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	record, err := svc.Get(ctx, issued.Credential.WorkerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record == nil || !record.Revoked() {
		t.Fatalf("expected revoked record, got %+v", record)
	}

	record, ok, err := svc.Verify(ctx, issued.Credential.WorkerID, issued.Token)
	if err != nil {
		t.Fatalf("Verify revoked: %v", err)
	}
	if ok {
		t.Fatal("expected revoked credential verification to fail")
	}
	if record == nil || !record.Revoked() {
		t.Fatalf("expected revoked record from Verify, got %+v", record)
	}
}

func TestListCredentials(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	records, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List empty: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected empty credential list, got %d", len(records))
	}

	for _, workerID := range []string{"worker-b", "worker-a"} {
		if _, err := svc.Create(ctx, IssueInput{
			WorkerID:  workerID,
			CreatedBy: "tester",
		}); err != nil {
			t.Fatalf("Create %s: %v", workerID, err)
		}
	}

	records, err = svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 credentials, got %d", len(records))
	}
	if got := []string{records[0].WorkerID, records[1].WorkerID}; !equalStrings(got, []string{"worker-a", "worker-b"}) {
		t.Fatalf("expected sorted worker ids, got %v", got)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
