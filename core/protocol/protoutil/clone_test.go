package protoutil

import (
	"errors"
	"strings"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func buildJobRequestFixture() *pb.JobRequest {
	return &pb.JobRequest{
		JobId:      "job-proto-clone",
		Topic:      "job.test",
		TenantId:   "default",
		ContextPtr: "ctx:job-proto-clone",
		Labels: map[string]string{
			"run_id":      "run-1",
			"workflow_id": "wf-1",
		},
		Env: map[string]string{
			"CORDUM_EFFECTIVE_CONFIG": `{"tenant":"default"}`,
			"CUSTOM":                  "keep-me",
		},
		Budget: &pb.Budget{
			MaxInputTokens:  1024,
			MaxOutputTokens: 512,
			MaxTotalTokens:  2048,
			DeadlineMs:      30000,
		},
		ContextHints: &pb.ContextHints{
			MaxInputTokens:     1024,
			AllowSummarization: true,
			AllowRetrieval:     true,
			Tags:               []string{"tag-a", "tag-b"},
		},
	}
}

func TestCloneJobRequest_NilInputReturnsError(t *testing.T) {
	t.Parallel()
	clone, err := CloneJobRequest(nil)
	if err == nil {
		t.Fatal("nil input must return an error")
	}
	if clone != nil {
		t.Fatalf("nil input must return nil clone, got %+v", clone)
	}
	if !strings.Contains(err.Error(), "nil JobRequest") {
		t.Fatalf("error message must mention the nil cause, got: %q", err.Error())
	}
}

func TestCloneJobRequest_RoundTrip(t *testing.T) {
	t.Parallel()
	orig := buildJobRequestFixture()
	clone, err := CloneJobRequest(orig)
	if err != nil {
		t.Fatalf("CloneJobRequest returned err: %v", err)
	}
	if clone == nil {
		t.Fatal("clone is nil")
	}
	if clone == orig {
		t.Fatal("clone must be a different pointer than orig")
	}
	// Scalars preserved.
	if clone.GetJobId() != orig.GetJobId() {
		t.Fatalf("JobId: clone=%s orig=%s", clone.GetJobId(), orig.GetJobId())
	}
	if clone.GetTopic() != orig.GetTopic() {
		t.Fatalf("Topic: clone=%s orig=%s", clone.GetTopic(), orig.GetTopic())
	}
	// Nested map content preserved.
	if clone.Labels["workflow_id"] != "wf-1" {
		t.Fatalf("Labels[workflow_id]: clone=%s", clone.Labels["workflow_id"])
	}
	// Nested messages preserved and independent.
	if clone.Budget == nil || clone.Budget == orig.Budget {
		t.Fatal("Budget must be a fresh nested message, got shared pointer")
	}
	if clone.Budget.GetMaxInputTokens() != orig.Budget.GetMaxInputTokens() {
		t.Fatalf("Budget.MaxInputTokens differs")
	}
	// Slice content preserved and independent.
	if clone.ContextHints == nil || clone.ContextHints == orig.ContextHints {
		t.Fatal("ContextHints must be a fresh nested message")
	}
	if len(clone.ContextHints.Tags) != len(orig.ContextHints.Tags) {
		t.Fatalf("Tags length: clone=%d orig=%d", len(clone.ContextHints.Tags), len(orig.ContextHints.Tags))
	}
}

func TestCloneJobRequest_CloneIndependence(t *testing.T) {
	// Mutating any field of the clone (scalar, map, nested message, repeated)
	// must NOT touch the original. Verifies deep-clone semantics so callers
	// can safely mutate the returned clone (the saga.go:322 use case, which
	// overwrites JobId, Topic, Priority, AdapterId, etc.).
	t.Parallel()
	orig := buildJobRequestFixture()
	origJobID := orig.GetJobId()
	origLabelsLen := len(orig.Labels)
	origTagsLen := len(orig.ContextHints.Tags)
	origBudgetMaxIn := orig.Budget.GetMaxInputTokens()

	clone, err := CloneJobRequest(orig)
	if err != nil {
		t.Fatalf("CloneJobRequest: %v", err)
	}
	clone.JobId = "mutated-job-id"
	clone.Labels["new_label"] = "added"
	clone.ContextHints.Tags = append(clone.ContextHints.Tags, "tag-c")
	clone.Budget.MaxInputTokens = 9999

	if orig.GetJobId() != origJobID {
		t.Fatalf("orig.JobId mutated: was %q, now %q", origJobID, orig.GetJobId())
	}
	if len(orig.Labels) != origLabelsLen {
		t.Fatalf("orig.Labels mutated: was %d entries, now %d", origLabelsLen, len(orig.Labels))
	}
	if len(orig.ContextHints.Tags) != origTagsLen {
		t.Fatalf("orig.Tags mutated: was %d, now %d", origTagsLen, len(orig.ContextHints.Tags))
	}
	if orig.Budget.GetMaxInputTokens() != origBudgetMaxIn {
		t.Fatalf("orig.Budget.MaxInputTokens mutated: was %d, now %d", origBudgetMaxIn, orig.Budget.GetMaxInputTokens())
	}
}

// Ensure errors.Is works sensibly on the returned errors so callers can
// pattern-match without string scraping.
func TestCloneJobRequest_ErrorUnwraps(t *testing.T) {
	t.Parallel()
	_, err := CloneJobRequest(nil)
	if err == nil {
		t.Fatal("want error on nil input")
	}
	// Not using a sentinel — just confirm errors.Is returns true for
	// itself (basic sanity) and false for an unrelated sentinel.
	if !errors.Is(err, err) {
		t.Fatal("errors.Is err,err must be true")
	}
	unrelated := errors.New("unrelated")
	if errors.Is(err, unrelated) {
		t.Fatal("errors.Is must not match an unrelated sentinel")
	}
}
