package scheduler

// Only WorkflowId, which the workflow engine alone sets, links a job to a
// workflow. The Safety Kernel's workflow policy scope and the workflow's saga
// come from it, never from a workflow_id label a client can write.

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// claimedWorkflowLabels claim wf-victim's policy scope under every label the
// Safety Kernel reads it from.
func claimedWorkflowLabels() map[string]string {
	return map[string]string{
		"workflow_id": "wf-victim", "workflow.id": "wf-victim", "workflow": "wf-victim",
		"workflow_run_id": "run-victim", "workflow.run_id": "run-victim", "team": "alpha",
	}
}

func TestSafetyCheckScopesPolicyByWorkflowID(t *testing.T) {
	conn, handler, cleanup := startInstrumentedTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_ALLOW, "")
	defer cleanup()
	client := &SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn, cb: newTestCB()}
	for _, tc := range []struct{ name, workflowID string }{
		{"client job claiming a workflow", ""},
		{"engine job", "wf-own"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.Check(context.Background(), &pb.JobRequest{
				JobId: "job-1", Topic: "job.default", WorkflowId: tc.workflowID, Labels: claimedWorkflowLabels(),
			}); err != nil {
				t.Fatalf("Check: %v", err)
			}
			labels := handler.lastReq.GetLabels()
			if labels["workflow_id"] != tc.workflowID || labels["team"] != "alpha" {
				t.Fatalf("kernel labels = %v, want workflow_id %q and the job's own labels", labels, tc.workflowID)
			}
			for _, k := range []string{"workflow.id", "workflow", "workflow_run_id", "workflow.run_id"} {
				if _, ok := labels[k]; ok {
					t.Fatalf("kernel saw claimed workflow scope %q: %v", k, labels)
				}
			}
		})
	}
}

func TestSagaRollbackFollowsWorkflowID(t *testing.T) {
	paths := map[string]func(*Engine, *pb.JobResult) error{
		"result": func(e *Engine, res *pb.JobResult) error {
			err := e.handleJobResult(res)
			e.wg.Wait() // the rollback runs in the background
			return err
		},
		"durable": func(e *Engine, res *pb.JobResult) error {
			return e.processDurableSaga(context.Background(), res)
		},
	}
	for _, tc := range []struct {
		name string
		req  *pb.JobRequest
		want int // wf-victim compensations dispatched
	}{
		{"engine job", &pb.JobRequest{JobId: "job-fatal", WorkflowId: "wf-victim"}, 1},
		{"client job labelled with the workflow", &pb.JobRequest{JobId: "job-fatal", Labels: claimedWorkflowLabels()}, 0},
	} {
		for path, fail := range paths {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				srv, err := miniredis.Run()
				if err != nil {
					t.Fatalf("miniredis: %v", err)
				}
				t.Cleanup(srv.Close)
				rdb, err := redisutil.NewClient("redis://" + srv.Addr())
				if err != nil {
					t.Fatalf("redis client: %v", err)
				}
				t.Cleanup(func() { _ = rdb.Close() })
				bus := &fakeBus{}
				saga := NewSagaManager(bus, rdb)
				// A wf-victim step succeeded and recorded its undo action.
				if err := saga.RecordCompensation(testCtx(t), &pb.JobRequest{
					JobId: "job-paid", Topic: "job.pay", WorkflowId: "wf-victim",
					Compensation: &pb.Compensation{Topic: "job.refund"},
				}); err != nil {
					t.Fatalf("record compensation: %v", err)
				}
				jobStore := newSagaJobStore()
				jobStore.reqs["job-fatal"] = tc.req
				engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).WithSaga(saga)

				if err := fail(engine, &pb.JobResult{JobId: "job-fatal", Status: pb.JobStatus_JOB_STATUS_FAILED_FATAL}); err != nil {
					t.Fatalf("fatal result: %v", err)
				}
				bus.mu.Lock()
				defer bus.mu.Unlock()
				n := 0
				for _, msg := range bus.published {
					if msg.subject == capsdk.SubjectSubmit && msg.packet.GetJobRequest().GetLabels()[sagaCompLabel] == "true" {
						n++
					}
				}
				if n != tc.want {
					t.Fatalf("dispatched %d wf-victim compensations, want %d", n, tc.want)
				}
			})
		}
	}
}
