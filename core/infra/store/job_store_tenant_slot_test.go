package store

// Evidence for task-8bab7c41: the tenant concurrency check and the claim on a
// slot are one atomic operation on job:tenant:active:<tenant>.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestReserveTenantSlotConcurrentReservationsAdmitExactlyCap(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	const reservations, limit = 24, 5
	start := make(chan struct{})
	var wg sync.WaitGroup
	reserved := make([]bool, reservations)
	errs := make([]error, reservations)
	for i := range reservations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reserved[i], _, errs[i] = store.ReserveTenantSlot(ctx, "tenant-a", fmt.Sprintf("job-%d", i), limit)
		}()
	}
	close(start)
	wg.Wait()
	admitted := 0
	for i := range reservations {
		if errs[i] != nil {
			t.Fatalf("ReserveTenantSlot(job-%d): %v", i, errs[i])
		}
		if reserved[i] {
			admitted++
		}
	}
	if admitted != limit {
		t.Fatalf("%d of %d concurrent reservations were admitted at cap %d, want exactly %d", admitted, reservations, limit, limit)
	}
	if active, err := store.CountActiveByTenant(ctx, "tenant-a"); err != nil || active != limit {
		t.Fatalf("CountActiveByTenant = %d, %v; want %d", active, err, limit)
	}
}

// Which jobs count: a PENDING or APPROVAL write joins the set only when the
// tenant is already in the job meta (DLQ retry, scheduler-written states), not
// when the state comes first (gateway HTTP and gRPC submit). The reservation
// counts every other member and never the job itself.
func TestReserveTenantSlotTenantActiveMembership(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	mustState := func(jobID string, state model.JobState) {
		t.Helper()
		if err := store.SetState(ctx, jobID, state); err != nil {
			t.Fatalf("SetState(%s, %s): %v", jobID, state, err)
		}
	}
	for _, state := range []model.JobState{model.JobStatePending, model.JobStateApproval} {
		stateFirst := "state-first-" + string(state)
		mustState(stateFirst, state)
		if err := store.SetTenant(ctx, stateFirst, "tenant-a"); err != nil {
			t.Fatalf("SetTenant: %v", err)
		}
		tenantFirst := "tenant-first-" + string(state)
		if err := store.SetJobMeta(ctx, &pb.JobRequest{JobId: tenantFirst, Topic: "job.default", TenantId: "tenant-a"}); err != nil {
			t.Fatalf("SetJobMeta: %v", err)
		}
		mustState(tenantFirst, state)
	}
	if active, err := store.CountActiveByTenant(ctx, "tenant-a"); err != nil || active != 2 {
		t.Fatalf("CountActiveByTenant = %d, %v; want 2 (the tenant-first PENDING and APPROVAL jobs)", active, err)
	}
	if ok, others, err := store.ReserveTenantSlot(ctx, "tenant-a", "tenant-first-"+string(model.JobStatePending), 2); err != nil || !ok || others != 1 {
		t.Fatalf("member reservation at cap 2 = %v, %d, %v; want admitted with 1 other job counted", ok, others, err)
	}
	if ok, others, err := store.ReserveTenantSlot(ctx, "tenant-a", "job-new", 2); err != nil || ok || others != 2 {
		t.Fatalf("new job at cap 2 = %v, %d, %v; want refused with 2 other jobs counted", ok, others, err)
	}
	if active, _ := store.CountActiveByTenant(ctx, "tenant-a"); active != 2 {
		t.Fatalf("a refused reservation joined the set: active = %d, want 2", active)
	}
	if ok, others, err := store.ReserveTenantSlot(ctx, "tenant-a", "job-new", 3); err != nil || !ok || others != 2 {
		t.Fatalf("new job at cap 3 = %v, %d, %v; want admitted with 2 other jobs counted", ok, others, err)
	}
	if active, _ := store.CountActiveByTenant(ctx, "tenant-a"); active != 3 {
		t.Fatalf("the admitted job is not counted: active = %d, want 3", active)
	}
	// A queued member that is refused leaves the set, so it holds no slot.
	if ok, others, err := store.ReserveTenantSlot(ctx, "tenant-a", "tenant-first-"+string(model.JobStateApproval), 2); err != nil || ok || others != 2 {
		t.Fatalf("member reservation at cap 2 = %v, %d, %v; want refused with 2 other jobs counted", ok, others, err)
	}
	if active, _ := store.CountActiveByTenant(ctx, "tenant-a"); active != 2 {
		t.Fatalf("a refused member kept its place: active = %d, want 2", active)
	}
}

func TestReserveTenantSlotFreedByReleaseAndTerminalState(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	reserve := func(jobID string, want bool) {
		t.Helper()
		if ok, _, err := store.ReserveTenantSlot(ctx, "tenant-a", jobID, 1); err != nil || ok != want {
			t.Fatalf("ReserveTenantSlot(%s) at cap 1 = %v, %v; want %v", jobID, ok, err, want)
		}
	}
	reserve("job-a", true)
	reserve("job-b", false)
	if err := store.ReleaseTenantSlot(ctx, "tenant-a", "job-a"); err != nil {
		t.Fatalf("ReleaseTenantSlot: %v", err)
	}
	reserve("job-b", true)
	// job-b reaches a terminal state the way a dispatched job does: its tenant
	// is in the meta, so the terminal write removes it from the set.
	if err := store.SetTenant(ctx, "job-b", "tenant-a"); err != nil {
		t.Fatalf("SetTenant: %v", err)
	}
	for _, state := range []model.JobState{model.JobStatePending, model.JobStateSucceeded} {
		if err := store.SetState(ctx, "job-b", state); err != nil {
			t.Fatalf("SetState(job-b, %s): %v", state, err)
		}
	}
	reserve("job-c", true)
}
