package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// busTapTenantKinds are the job packets the taps' tenant rule covers, each
// with its sys.job subject. A request is for job in tenant; progress and
// cancel name job.
var busTapTenantKinds = map[string]struct {
	subject string
	set     func(p *pb.BusPacket, job, tenant string)
}{
	"progress": {capsdk.SubjectProgress, func(p *pb.BusPacket, job, _ string) {
		p.Payload = &pb.BusPacket_JobProgress{JobProgress: &pb.JobProgress{JobId: job, Percent: 50, Message: "progress"}}
	}},
	"cancel": {capsdk.SubjectCancel, func(p *pb.BusPacket, job, _ string) {
		p.Payload = &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{JobId: job, Reason: "cancel"}}
	}},
	"request": {capsdk.SubjectSubmit, func(p *pb.BusPacket, job, tenant string) {
		p.Payload = &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: job, TenantId: tenant, Topic: "job.default"}}
	}},
}

// TestBusTapTenantJobPackets checks the tenant rule for a worker session's job
// request, progress and cancel on the gateway's sys.job.> and sys.audit.> taps:
// such a packet reaches a stream listener only when it belongs to the session's
// tenant. Any other is acknowledged and has no effect. Tenant B's own worker
// still reaches B's listener and the platform listener.
func TestBusTapTenantJobPackets(t *testing.T) {
	for _, c := range []struct {
		name       string
		mode       scheduler.HandshakeMode
		production bool
	}{
		{"enforce", scheduler.HandshakeModeEnforce, false},
		{"production", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, bus := busTapGateway(t, c.mode, c.production)
			const jobB = "job-tenant-b"
			if err := s.jobStore.SetJobMeta(context.Background(), &pb.JobRequest{JobId: jobB, TenantId: "tenant-b"}); err != nil {
				t.Fatal(err)
			}
			listeners := []string{"tenant-a", "tenant-b", "platform"}
			clients := map[string]*wsClient{
				"tenant-a": registerGatewayEdgeStreamClient(t, s, "tenant-a", false),
				"tenant-b": registerGatewayEdgeStreamClient(t, s, "tenant-b", false),
				"platform": registerGatewayEdgeStreamClient(t, s, "", true),
			}
			// The platform listener is read last: it also gets the tenant listeners' sentinels.
			relayed := func(job string) map[string]int {
				got := map[string]int{}
				for _, name := range listeners {
					got[name] = (&audienceMatrix{s: s}).matrixRelayed(t, clients[name], job)
				}
				return got
			}
			workerA := busTapTenantSender(t, s, "worker-a", "tenant-a")
			workerB := busTapTenantSender(t, s, "worker-b", "tenant-b")

			for _, r := range []struct{ kind, label, job, tenant string }{
				{"progress", "tenant-b's job", jobB, ""},
				{"cancel", "tenant-b's job", jobB, ""},
				{"request", "tenant-b", "job-child-b", "tenant-b"},
				{"progress", "a job with no record", "job-no-record", ""},
			} {
				kind := busTapTenantKinds[r.kind]
				for _, subject := range []string{kind.subject, "sys.audit.output_policy"} {
					label := "worker-a " + r.kind + " for " + r.label + " on " + subject
					handler := busTapHandler(t, bus, subject)
					keys, published := snapshotPackKeyspace(t, s), storeWriterPublished(bus)
					if err := handler(workerA(kind.set, r.job, r.tenant)); err != nil {
						t.Errorf("%s: handler returned %v, want nil", label, err)
					}
					if diff := diffPackKeyspace(keys, snapshotPackKeyspace(t, s)); len(diff) != 0 {
						t.Errorf("%s: keys changed: %v", label, diff)
					}
					if n := storeWriterPublished(bus) - published; n != 0 {
						t.Errorf("%s: published %d messages", label, n)
					}
					if got := relayed(r.job); got["tenant-a"]+got["tenant-b"]+got["platform"] != 0 {
						t.Errorf("%s: relayed %v, want none", label, got)
					}
				}
			}

			for _, r := range []struct{ kind, job string }{
				{"progress", jobB}, {"cancel", jobB}, {"request", "job-child-b-owner"},
			} {
				kind := busTapTenantKinds[r.kind]
				for _, subject := range []string{kind.subject, "sys.audit.output_policy"} {
					label := "worker-b " + r.kind + " for tenant-b on " + subject
					if err := busTapHandler(t, bus, subject)(workerB(kind.set, r.job, "tenant-b")); err != nil {
						t.Errorf("%s: handler returned %v, want nil", label, err)
					}
					if got := relayed(r.job); got["tenant-a"] != 0 || got["tenant-b"] != 1 || got["platform"] != 1 {
						t.Errorf("%s: relayed %v, want tenant-b and platform once each", label, got)
					}
				}
			}
		})
	}
}

// busTapTenantSender mints worker's bound session in tenant once and returns a
// builder for that worker's job packets.
func busTapTenantSender(t *testing.T, s *server, worker, tenant string) func(set func(*pb.BusPacket, string, string), job, jobTenant string) *pb.BusPacket {
	t.Helper()
	token, _, err := s.sessionIssuer.IssueBound(context.Background(), scheduler.SessionBinding{
		WorkerID: worker, AgentID: "agent-" + worker, Tenant: tenant,
		Audience: scheduler.WorkerHandshakeAudience, ProofKeyID: "proof-" + worker, SDKVersion: "test-sdk",
	})
	if err != nil {
		t.Fatalf("session for %s: %v", worker, err)
	}
	return func(set func(*pb.BusPacket, string, string), job, jobTenant string) *pb.BusPacket {
		p := &pb.BusPacket{SenderId: worker, AuthToken: token}
		set(p, job, jobTenant)
		return p
	}
}

// busTapHandler returns the one tap handler subscribed to subject.
func busTapHandler(t *testing.T, bus *stubBus, subject string) func(*pb.BusPacket) error {
	t.Helper()
	bus.mu.Lock()
	defer bus.mu.Unlock()
	var handlers []func(*pb.BusPacket) error
	for pattern, subs := range bus.subs {
		if subjectMatches(pattern, subject) {
			handlers = append(handlers, subs...)
		}
	}
	if len(handlers) != 1 {
		t.Fatalf("%s handlers=%d, want 1", subject, len(handlers))
	}
	return handlers[0]
}
