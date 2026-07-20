package store

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/model"
)

func TestRuntimeFenceMigrationNeverOverwritesNewerBinding(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-migrate-fence"
	if err := store.client.HSet(ctx, jobMetaKey(jobID),
		runtimeStateField, string(model.JobStateRunning),
		metaFieldDispatchID, "legacy-dispatch", metaFieldDispatchAttempt, "1",
		metaFieldDispatchWorkerID, "legacy-worker", metaFieldDispatchTenant, "tenant-a",
	).Err(); err != nil {
		t.Fatalf("seed legacy fence error = %v", err)
	}
	if err := store.client.HSet(ctx, jobRuntimeKey(jobID),
		metaFieldDispatchID, "new-dispatch", metaFieldDispatchAttempt, "2",
		metaFieldDispatchWorkerID, "new-worker", metaFieldDispatchTenant, "tenant-a",
	).Err(); err != nil {
		t.Fatalf("seed runtime fence error = %v", err)
	}
	if err := store.migrateRuntimeFence(ctx, jobID); err != nil {
		t.Fatalf("migrateRuntimeFence() error = %v", err)
	}
	fields, err := store.client.HGetAll(ctx, jobRuntimeKey(jobID)).Result()
	if err != nil {
		t.Fatalf("HGetAll(runtime) error = %v", err)
	}
	if fields[metaFieldDispatchID] != "new-dispatch" || fields[metaFieldDispatchAttempt] != "2" ||
		fields[metaFieldDispatchWorkerID] != "new-worker" || fields[runtimeStateField] != string(model.JobStateRunning) {
		t.Fatalf("migrated runtime fence = %+v, want newer binding plus legacy state", fields)
	}
}
