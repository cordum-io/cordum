package scheduler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/bus"
	coresdk "github.com/cordum/cordum/core/protocol/capsdk"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tests/identity"
	"github.com/redis/go-redis/v9"
)

// matrixSettle bounds the wait for an asynchronous effect: the config refresh
// runs after its handler returns, so a refused cell waits the full window.
const matrixSettle = 500 * time.Millisecond

const matrixDispatchSubject = "worker." + matrixWorkerID + ".jobs"

// TestAudienceMatrixBusJobSubmit presents every credential on sys.job.submit,
// signed as the gateway through CAP-PRODUCTION raw admission. The protected
// effect is a stored job and its dispatch to the enrolled worker.
func TestAudienceMatrixBusJobSubmit(t *testing.T) {
	identity.Run(t, identity.BusJobSubmit, func(t *testing.T, c identity.Credential) identity.Result {
		f := newBusMatrixFixture(t, true)
		const jobID = "job-matrix-submit"
		request, err := jobidentity.NormalizeProductionJobRequest(&pb.JobRequest{JobId: jobID, Topic: matrixTopic}, matrixJobIdentity())
		if err != nil {
			t.Fatalf("normalize job request: %v", err)
		}
		packet := f.packet(t, c, servicetoken.IdentityGateway)
		packet.Payload = &pb.BusPacket_JobRequest{JobRequest: request}
		return matrixResult(observe(
			func() bus.RawAdmissionDisposition {
				return f.present(t, capsdk.SubjectSubmit, servicetoken.IdentityGateway, packet)
			},
			matrixProbe{"sys.job.submit job store", func() string { return f.jobState(t, jobID) }},
			matrixProbe{"sys.job.submit dispatch", func() string { return f.bus.publishes(matrixDispatchSubject) }},
		)...)
	})
}

// TestAudienceMatrixBusControl presents every credential on the control plane's
// other subjects, each the packet the gateway sends: a cancel, a worker config
// change notification, and the control-plane cancel result on sys.job.result.
// The baseline is the gateway's cancel and config change with its service token.
func TestAudienceMatrixBusControl(t *testing.T) {
	gateway := servicetoken.IdentityGateway
	identity.Run(t, identity.BusControl, func(t *testing.T, c identity.Credential) identity.Result {
		f := newBusMatrixFixture(t, true)
		observations := f.controlCancelAndConfig(t, c)
		f.dispatchedJob(t, "job-matrix-control-result")
		result := f.packet(t, c, gateway)
		result.Payload = &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: "job-matrix-control-result", WorkerId: gateway, Status: pb.JobStatus_JOB_STATUS_CANCELLED, Identity: matrixJobIdentity(),
		}}
		observations = append(observations, observe(
			func() bus.RawAdmissionDisposition { return f.present(t, capsdk.SubjectResult, gateway, result) },
			matrixProbe{"sys.job.result control-plane job store", func() string { return f.jobState(t, "job-matrix-control-result") }},
		)...)
		return matrixResult(observations...)
	}, identity.Skip(identity.ServiceToken, "PR1-21"), identity.Baseline(func(t *testing.T) identity.Result {
		return matrixResult(newBusMatrixFixture(t, true).controlCancelAndConfig(t, identity.ServiceToken)...)
	}))
}

// controlCancelAndConfig presents the gateway's cancel and config change
// notification with credential c and snapshots each one's protected state.
func (f *busMatrixFixture) controlCancelAndConfig(t *testing.T, c identity.Credential) []matrixObservation {
	gateway := servicetoken.IdentityGateway
	f.dispatchedJob(t, "job-matrix-cancel")
	cancel := f.packet(t, c, gateway)
	cancel.Payload = &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{
		JobId: "job-matrix-cancel", Reason: "cancelled via api", RequestedBy: gateway, Identity: matrixJobIdentity(),
	}}
	config := f.packet(t, c, gateway)
	config.Payload = &pb.BusPacket_Alert{Alert: &pb.SystemAlert{
		Message: "config changed", SourceComponent: gateway,
		Details: map[string]string{"scope": string(configsvc.ScopeSystem), "scope_id": "workers"},
	}}
	observations := observe(
		func() bus.RawAdmissionDisposition { return f.present(t, capsdk.SubjectCancel, gateway, cancel) },
		matrixProbe{"sys.job.cancel job store", func() string { return f.jobState(t, "job-matrix-cancel") }},
	)
	refreshes := func() string { return fmt.Sprintf("%d credential refreshes", f.refreshes.Load()) }
	return append(observations, observe(
		func() bus.RawAdmissionDisposition {
			before := f.refreshes.Load()
			disposition := f.present(t, coresdk.SubjectConfigChanged, gateway, config)
			f.settle(func() bool { return f.refreshes.Load() != before })
			return disposition
		},
		matrixProbe{"sys.config.changed worker config", refreshes},
	)...)
}

// TestAudienceMatrixBusWorker presents every credential on what an enrolled
// worker that completed its handshake sends about itself: a heartbeat, a
// capability announcement, job progress and its own job result.
func TestAudienceMatrixBusWorker(t *testing.T) {
	identity.Run(t, identity.BusWorker, func(t *testing.T, c identity.Credential) identity.Result {
		f := newBusMatrixFixture(t, false)
		progressFence := f.dispatchedJob(t, "job-matrix-progress")
		resultFence := f.dispatchedJob(t, "job-matrix-result")
		send := func(subject string, packet *pb.BusPacket) func() bus.RawAdmissionDisposition {
			return func() bus.RawAdmissionDisposition { return f.present(t, subject, matrixWorkerID, packet) }
		}

		heartbeat := f.packet(t, c, matrixWorkerID)
		heartbeat.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: matrixWorkerID, Pool: matrixPool, MaxParallelJobs: 1}}
		capability := f.packet(t, c, matrixWorkerID)
		capability.Payload = &pb.BusPacket_Handshake{Handshake: &pb.Handshake{
			ComponentId: matrixWorkerID, Role: pb.ComponentRole_COMPONENT_ROLE_WORKER, SupportedVersions: []int32{capsdk.DefaultProtocolVersion},
			SdkVersion: matrixBinding().SDKVersion, ReadyTopics: []string{matrixTopic},
		}}
		progress := f.packet(t, c, matrixWorkerID)
		progress.Payload = &pb.BusPacket_JobProgress{JobProgress: &pb.JobProgress{
			JobId: "job-matrix-progress", Percent: 50, Message: "halfway", Dispatch: progressFence, Identity: matrixJobIdentity(),
		}}
		result := f.packet(t, c, matrixWorkerID)
		result.Payload = &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: "job-matrix-result", WorkerId: matrixWorkerID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
			Dispatch: resultFence, Identity: matrixJobIdentity(),
		}}

		observations := observe(send(capsdk.SubjectHeartbeat, heartbeat), matrixProbe{"sys.heartbeat worker registry", func() string {
			return fmt.Sprintf("registered=%t", f.registry.Snapshot()[matrixWorkerID] != nil)
		}})
		observations = append(observations, observe(send(capsdk.SubjectHandshake, capability), matrixProbe{"sys.handshake trusted capability", func() string {
			return fmt.Sprintf("trusted=%t", f.registry.ReadinessSnapshot()[matrixWorkerID].Trusted)
		}})...)
		observations = append(observations, observe(send(capsdk.SubjectProgress, progress),
			matrixProbe{"sys.job.progress job store", func() string { return f.jobEvents(t, "job-matrix-progress") }},
			matrixProbe{"sys.job.progress accepted stream", func() string { return f.bus.publishes(coresdk.SubjectAcceptedProgress) }},
		)...)
		observations = append(observations, observe(send(capsdk.SubjectResult, result),
			matrixProbe{"sys.job.result worker job store", func() string { return f.jobState(t, "job-matrix-result") }},
		)...)
		return matrixResult(observations...)
	})
}

// jobState is the job's stored state, or "absent" when the store has none.
func (f *busMatrixFixture) jobState(t *testing.T, jobID string) string {
	t.Helper()
	state, err := f.jobs.GetState(context.Background(), jobID)
	if errors.Is(err, redis.Nil) {
		return "absent"
	}
	if err != nil {
		t.Fatalf("read %s state: %v", jobID, err)
	}
	return string(state)
}

// jobEvents counts the signed job events the durable store accepted for jobID
// (RedisJobStore keeps them as event:* fields of the job's runtime hash).
func (f *busMatrixFixture) jobEvents(t *testing.T, jobID string) string {
	t.Helper()
	key := "job:{" + base64.RawURLEncoding.EncodeToString([]byte(jobID)) + "}:runtime"
	fields, err := f.kit.rdb.HKeys(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("read %s runtime: %v", jobID, err)
	}
	events := 0
	for _, field := range fields {
		if strings.HasPrefix(field, "event:") {
			events++
		}
	}
	return fmt.Sprintf("%d accepted events", events)
}

// sender is the packet sender for c: agent_id_header claims the worker's agent id.
func (k *matrixKit) sender(c identity.Credential, normal string) string {
	if c == identity.AgentIDHeader {
		return k.binding.AgentID
	}
	return normal
}

// matrixProductionMode sets the production profile the way cordum-scheduler
// boots it: handshake enforce, set explicitly, with WORKER_ATTESTATION off
// (worker_security_modes.go refuses both active), and returns the parsed mode.
func matrixProductionMode(t *testing.T) HandshakeMode {
	t.Helper()
	t.Setenv(EnvHandshakeMode, "enforce")
	t.Setenv(EnvWorkerAttestation, "off")
	mode, err := ParseHandshakeModeStrict(os.Getenv(EnvHandshakeMode))
	attestation, attErr := ParseWorkerAttestationModeStrict(os.Getenv(EnvWorkerAttestation))
	if err != nil || attErr != nil || mode != HandshakeModeEnforce || attestation.Enabled() {
		t.Fatalf("production profile: mode=%q err=%v attestation=%q err=%v", mode, err, attestation, attErr)
	}
	return mode
}

// matrixProbe reads one protected state a subject can change.
type matrixProbe struct {
	name string
	read func() string
}

// matrixObservation is one probe's before/after snapshot around a packet.
type matrixObservation struct {
	subject       string
	disposition   bus.RawAdmissionDisposition
	before, after string
}

// observe snapshots every probe, presents one packet, then snapshots again.
func observe(present func() bus.RawAdmissionDisposition, probes ...matrixProbe) []matrixObservation {
	before := make([]string, len(probes))
	for i, probe := range probes {
		before[i] = probe.read()
	}
	disposition := present()
	observations := make([]matrixObservation, len(probes))
	for i, probe := range probes {
		observations[i] = matrixObservation{subject: probe.name, disposition: disposition, before: before[i], after: probe.read()}
	}
	return observations
}

var matrixDispositions = map[bus.RawAdmissionDisposition]string{
	bus.RawAdmissionInvalid: "invalid", bus.RawAdmissionAccepted: "accepted", bus.RawAdmissionDuplicate: "duplicate",
	bus.RawAdmissionRejected: "rejected", bus.RawAdmissionRetry: "retry",
}

// matrixResult aggregates per-subject snapshots by the rule of record: Accepted
// with an Effect only when every subject changed; an Effect when any did.
func matrixResult(observations ...matrixObservation) identity.Result {
	all, some := true, false
	evidence := make([]string, 0, len(observations))
	for _, o := range observations {
		changed := o.before != o.after
		all, some = all && changed, some || changed
		evidence = append(evidence, fmt.Sprintf("%s: admission %s, snapshot %s -> %s", o.subject, matrixDispositions[o.disposition], o.before, o.after))
	}
	return identity.Result{Accepted: all, Effect: some, Evidence: "state snapshots (Accepted from the same snapshots); " + strings.Join(evidence, "; ")}
}

// settle polls until changed reports true or matrixSettle elapses.
func (f *busMatrixFixture) settle(changed func() bool) {
	deadline := time.Now().Add(matrixSettle)
	for !changed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}
