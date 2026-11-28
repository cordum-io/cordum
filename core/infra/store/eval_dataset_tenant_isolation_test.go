package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/cordum/cordum/core/model"
	"github.com/google/uuid"
)

// TestTenantIsolationEvalDatasetByName seeds tenant B's dataset, then looks it
// up by name and version as tenant A, which knows B's dataset name.
func TestTenantIsolationEvalDatasetByName(t *testing.T) {
	const (
		tenantA, tenantB = "tenant-iso-a", "tenant-iso-b"
	)
	name := "iso-dataset-" + uuid.NewString()
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()
	dataset := sampleDataset(name, 1, tenantB)
	dataset.Entries[0].Input = json.RawMessage(`{"input":"ISO-eval-` + uuid.NewString() + `"}`)
	created, err := s.CreateEvalDataset(ctx, dataset)
	if err != nil {
		t.Fatalf("seed tenant B dataset: %v", err)
	}

	// Control: tenant B reaches the dataset through the same lookups.
	before, err := s.GetEvalDatasetByNameVersion(ctx, tenantB, name, 1)
	if err != nil || !reflect.DeepEqual(before, created) {
		t.Fatalf("tenant B by-name lookup = %q, %v", before.ID, err)
	}
	if got, err := s.ListEvalDatasetVersions(ctx, tenantB, name); err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], created) {
		t.Fatalf("tenant B versions = %d, %v", len(got), err)
	}

	got, err := s.GetEvalDatasetByNameVersion(ctx, tenantA, name, 1)
	if !errors.Is(err, ErrEvalDatasetNotFound) || !reflect.DeepEqual(got, model.EvalDataset{}) {
		t.Fatalf("tenant A by-name lookup = %+v, %v; want ErrEvalDatasetNotFound and no data", got, err)
	}
	versions, err := s.ListEvalDatasetVersions(ctx, tenantA, name)
	if err != nil || len(versions) != 0 {
		t.Fatalf("tenant A versions = %d, %v; want none", len(versions), err)
	}
	if got, err := s.GetEvalDataset(ctx, tenantA, created.ID); !errors.Is(err, ErrEvalDatasetNotFound) || got.ID != "" {
		t.Fatalf("tenant A lookup by tenant B's id = %+v, %v", got, err)
	}

	after, err := s.GetEvalDatasetByNameVersion(ctx, tenantB, name, 1)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("tenant B dataset changed: %v", err)
	}
	if got, err := s.ListEvalDatasetVersions(ctx, tenantB, name); err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], before) {
		t.Fatalf("tenant B versions after probes = %d, %v", len(got), err)
	}
}
