package scheduler

// Coverage for task-948d913b: scheduler attaches a verifiable control-plane
// service token to its internal broadcasts, and attaches nothing when the gate
// is disabled (back-compat).

import (
	"errors"
	"slices"
	"sync"
	"testing"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestAttachServiceToken_AttachesVerifiableSchedulerToken(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	e := &Engine{sessionMiddleware: NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())}
	pkt := &pb.BusPacket{}
	e.attachServiceToken(pkt)
	if pkt.AuthToken == "" {
		t.Fatal("attachServiceToken must set a token under enforce")
	}
	claims, err := issuer.VerifyService(pkt.AuthToken)
	if err != nil {
		t.Fatalf("attached token must verify as a service token: %v", err)
	}
	if claims.Subject != defaultSenderID {
		t.Fatalf("attached token subject = %q, want %q", claims.Subject, defaultSenderID)
	}
}

func TestAttachServiceToken_NoTokenWhenGateDisabled(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	// Off mode: MintServiceToken returns ("",nil) so no token is attached — a
	// peer with the gate disabled admits token-less internal broadcasts anyway.
	eOff := &Engine{sessionMiddleware: NewSessionTokenMiddleware(issuer, HandshakeModeOff, NewHandshakeMissingTracker())}
	offPkt := &pb.BusPacket{}
	eOff.attachServiceToken(offPkt)
	if offPkt.AuthToken != "" {
		t.Fatalf("Off mode must not attach a token; got %q", offPkt.AuthToken)
	}
	// nil middleware: no token, no panic.
	eNil := &Engine{}
	nilPkt := &pb.BusPacket{}
	eNil.attachServiceToken(nilPkt)
	if nilPkt.AuthToken != "" {
		t.Fatal("nil middleware must not attach a token")
	}
}

// publishCapture snapshots each packet at Publish entry. DLQAndResult reuses
// the DLQ packet pointer for the result publish, so a pointer-retaining fake
// would let the later result token stamp the captured DLQ retroactively.
type publishCapture struct {
	mu        sync.Mutex
	published []publishedMsg
}

func (b *publishCapture) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, publishedMsg{subject: subject, packet: proto.Clone(packet).(*pb.BusPacket)})
	return nil
}

func (b *publishCapture) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

type schedulerPublisherSite struct {
	name    string
	sender  string   // SenderId when no token is attached
	emits   []string // every subject published, in order; the first is the target
	publish func(t *testing.T, bus Bus, mw *SessionTokenMiddleware)
}

func engineSite(name string, emits []string, run func(*Engine) error) schedulerPublisherSite {
	return schedulerPublisherSite{name: name, sender: defaultSenderID, emits: emits,
		publish: func(t *testing.T, bus Bus, mw *SessionTokenMiddleware) {
			if err := run(&Engine{bus: bus, sessionMiddleware: mw}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}}
}

func sagaSite(name string, run func(*SagaManager)) schedulerPublisherSite {
	return schedulerPublisherSite{name: name, sender: sagaSenderID, emits: []string{capsdk.SubjectDLQ},
		publish: func(_ *testing.T, bus Bus, mw *SessionTokenMiddleware) {
			run(NewSagaManager(bus, nil).WithSessionMiddleware(mw))
		}}
}

// payloadOnly drops the envelope fields a publisher may set per publish so two
// runs of one site compare on everything else.
func payloadOnly(pkt *pb.BusPacket) *pb.BusPacket {
	out := proto.Clone(pkt).(*pb.BusPacket)
	out.AuthToken, out.SenderId, out.TraceId, out.CreatedAt = "", "", "", nil
	return out
}

type publisherMode struct {
	name   string
	mw     *SessionTokenMiddleware
	signed bool
}

func runSchedulerPublisherSites(t *testing.T, sites []schedulerPublisherSite) {
	t.Helper()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	t.Cleanup(cleanup)
	modes := []publisherMode{
		{"warn", NewSessionTokenMiddleware(issuer, HandshakeModeWarn, NewHandshakeMissingTracker()), true},
		{"enforce", NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker()), true},
		{"off", NewSessionTokenMiddleware(issuer, HandshakeModeOff, NewHandshakeMissingTracker()), false},
		// Active mode with no issuer: mint fails, the packet still goes out token-less.
		{"enforce_mint_failure", NewSessionTokenMiddleware(nil, HandshakeModeEnforce, NewHandshakeMissingTracker()), false},
	}
	for _, site := range sites {
		t.Run(site.name, func(t *testing.T) { runSchedulerPublisherSite(t, issuer, site, modes) })
	}
}

func runSchedulerPublisherSite(t *testing.T, issuer *SessionTokenIssuer, site schedulerPublisherSite, modes []publisherMode) {
	var firstPayload *pb.BusPacket
	for _, mode := range modes {
		bus := &publishCapture{}
		site.publish(t, bus, mode.mw)
		var subjects []string
		for _, m := range bus.published {
			subjects = append(subjects, m.subject)
		}
		if !slices.Equal(subjects, site.emits) {
			t.Fatalf("%s published %v, want %v", mode.name, subjects, site.emits)
		}
		pkt := bus.published[0].packet
		if !mode.signed {
			if pkt.GetAuthToken() != "" || pkt.GetSenderId() != site.sender {
				t.Fatalf("%s token=%q sender=%q, want no token and sender %q",
					mode.name, pkt.GetAuthToken(), pkt.GetSenderId(), site.sender)
			}
		} else {
			claims, err := issuer.VerifyService(pkt.GetAuthToken())
			if err != nil {
				t.Fatalf("%s token does not verify: %v", mode.name, err)
			}
			if claims.Subject != defaultSenderID || pkt.GetSenderId() != defaultSenderID {
				t.Fatalf("%s subject=%q sender=%q, want %q", mode.name, claims.Subject, pkt.GetSenderId(), defaultSenderID)
			}
		}
		if firstPayload == nil {
			firstPayload = payloadOnly(pkt)
		} else if !proto.Equal(payloadOnly(pkt), firstPayload) {
			t.Fatalf("%s payload changed: got %v, want %v", mode.name, payloadOnly(pkt), firstPayload)
		}
	}
}

func TestServiceTokenDLQPublishers(t *testing.T) {
	t.Parallel()
	dlq := []string{capsdk.SubjectDLQ}
	req := func() *pb.JobRequest { return &pb.JobRequest{JobId: "job-replay", Topic: "job.test"} }
	runSchedulerPublisherSites(t, []schedulerPublisherSite{
		engineSite("emitDLQ", dlq, func(e *Engine) error {
			return e.emitDLQ("job-dlq", "job.test", pb.JobStatus_JOB_STATUS_FAILED_FATAL, "retries exhausted", "max_scheduling_retries")
		}),
		engineSite("replayApprovalPublish_dlq", dlq, func(e *Engine) error {
			return e.replayApprovalPublish("trace-replay", req(), ApprovalRecord{PublishTarget: ApprovalPublishTargetDLQ, Reason: "rejected"})
		}),
		engineSite("replayApprovalPublish_dlq_and_result", []string{capsdk.SubjectDLQ, capsdk.SubjectResult}, func(e *Engine) error {
			return e.replayApprovalPublish("trace-replay", req(), ApprovalRecord{PublishTarget: ApprovalPublishTargetDLQAndResult, Reason: "rejected"})
		}),
		sagaSite("sendBrokenCompensationToDLQ", func(s *SagaManager) {
			s.sendBrokenCompensationToDLQ("wf-1", []byte("{"), errors.New("unexpected end of JSON input"))
		}),
		sagaSite("sendCompensationSafetyFailureToDLQ", func(s *SagaManager) {
			s.sendCompensationSafetyFailureToDLQ("wf-1", &pb.JobRequest{JobId: "comp-1", Topic: "job.undo"}, ErrSafetyUnavailable)
		}),
	})
}

func TestServiceTokenOutputAuditPublisher(t *testing.T) {
	t.Parallel()
	runSchedulerPublisherSites(t, []schedulerPublisherSite{
		engineSite("emitOutputAuditEvent", []string{outputPolicyAudit}, func(e *Engine) error {
			e.emitOutputAuditEvent("job-out", "job.test", "redacted", "secret removed", OutputRedact)
			return nil
		}),
	})
}
