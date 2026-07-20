package resourceio

import (
	"errors"
	"testing"

	"github.com/cordum/cordum/core/infra/resource"
)

func TestBoundLegacyKeyRequiresExactTrustedJobScope(t *testing.T) {
	trusted := resource.TrustedContext{TenantID: "tenant-a", JobID: "job-a"}
	for name, test := range map[string]struct {
		pointer string
		kind    LegacyKind
		want    string
	}{
		"context":         {pointer: "redis://ctx:job-a", kind: LegacyContext, want: "ctx:job-a"},
		"result":          {pointer: "redis://res:job-a", kind: LegacyResult, want: "res:job-a"},
		"cross job":       {pointer: "redis://res:job-b", kind: LegacyResult},
		"wrong namespace": {pointer: "redis://ctx:job-a", kind: LegacyResult},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := BoundLegacyKey(test.pointer, test.kind, trusted)
			if test.want == "" {
				if !errors.Is(err, ErrLegacyScopeMismatch) {
					t.Fatalf("BoundLegacyKey error = %v, want ErrLegacyScopeMismatch", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("BoundLegacyKey = %q, %v", got, err)
			}
		})
	}
}

func TestBoundLegacyKeyAllowsCanonicalWorkflowJobID(t *testing.T) {
	trusted := resource.TrustedContext{TenantID: "tenant-a", JobID: "run:loop[0]@2"}
	got, err := BoundLegacyKey("redis://res:run:loop[0]@2", LegacyResult, trusted)
	if err != nil || got != "res:run:loop[0]@2" {
		t.Fatalf("BoundLegacyKey = %q, %v", got, err)
	}
}
