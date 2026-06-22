package runtime

import (
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type capturePublisher struct {
	subject string
	data    []byte
	err     error
}

func (p *capturePublisher) Publish(subject string, data []byte) error {
	p.subject = subject
	p.data = append([]byte(nil), data...)
	return p.err
}

func TestDirectSubject(t *testing.T) {
	if got := DirectSubject(""); got != "" {
		t.Fatalf("expected empty subject for empty worker id, got %q", got)
	}
	if got := DirectSubject(" worker-1 "); got != "worker.worker-1.jobs" {
		t.Fatalf("unexpected direct subject: %q", got)
	}
}

func TestPublishCancel(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pub := &capturePublisher{}
		cancel := &agentv1.JobCancel{
			JobId:       "job-9",
			Reason:      "stop",
			RequestedBy: "user-1",
		}

		if err := PublishCancel(pub, cancel, "trace-9", "gateway-1", nil); err != nil {
			t.Fatalf("PublishCancel failed: %v", err)
		}
		if pub.subject != SubjectCancel {
			t.Fatalf("expected subject %q, got %q", SubjectCancel, pub.subject)
		}

		var pkt agentv1.BusPacket
		if err := proto.Unmarshal(pub.data, &pkt); err != nil {
			t.Fatalf("unmarshal packet: %v", err)
		}
		if pkt.GetTraceId() != "trace-9" {
			t.Fatalf("expected trace id trace-9, got %q", pkt.GetTraceId())
		}
		if pkt.GetSenderId() != "gateway-1" {
			t.Fatalf("expected sender id gateway-1, got %q", pkt.GetSenderId())
		}
		cancelMsg := pkt.GetJobCancel()
		if cancelMsg == nil {
			t.Fatalf("expected job cancel payload")
		}
		if cancelMsg.GetJobId() != "job-9" || cancelMsg.GetReason() != "stop" {
			t.Fatalf("unexpected cancel payload: %#v", cancelMsg)
		}
	})

	t.Run("validation", func(t *testing.T) {
		if err := PublishCancel(nil, &agentv1.JobCancel{JobId: "job-9"}, "trace", "gateway-1", nil); err == nil {
			t.Fatalf("expected error for nil publisher")
		}
		if err := PublishCancel(&capturePublisher{}, nil, "trace", "gateway-1", nil); err == nil {
			t.Fatalf("expected error for nil cancel")
		}
		if err := PublishCancel(&capturePublisher{}, &agentv1.JobCancel{}, "trace", "gateway-1", nil); err == nil {
			t.Fatalf("expected error for empty job id")
		}
		if err := PublishCancel(&capturePublisher{}, &agentv1.JobCancel{JobId: "job-9"}, "trace", "", nil); err == nil {
			t.Fatalf("expected error for empty sender id")
		}
	})
}

func TestHeartbeatPayloadWithAuthToken(t *testing.T) {
	payload, err := HeartbeatPayload("worker-auth", "pool-auth", 2, 8, 12.5, WithAuthToken(" attestation-token "))
	if err != nil {
		t.Fatalf("HeartbeatPayload failed: %v", err)
	}

	var pkt agentv1.BusPacket
	if err := proto.Unmarshal(payload, &pkt); err != nil {
		t.Fatalf("unmarshal packet: %v", err)
	}
	hb := pkt.GetHeartbeat()
	if hb == nil {
		t.Fatal("expected heartbeat payload")
	}
	if hb.AuthToken != "attestation-token" {
		t.Fatalf("auth_token = %q, want %q", hb.AuthToken, "attestation-token")
	}
}

func unknownStringField(raw []byte, fieldNumber protowire.Number) (string, bool) {
	for len(raw) > 0 {
		num, wireType, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			return "", false
		}
		raw = raw[tagLen:]
		if num == fieldNumber && wireType == protowire.BytesType {
			value, valueLen := protowire.ConsumeString(raw)
			if valueLen < 0 {
				return "", false
			}
			return value, true
		}
		valueLen := protowire.ConsumeFieldValue(num, wireType, raw)
		if valueLen < 0 {
			return "", false
		}
		raw = raw[valueLen:]
	}
	return "", false
}

// TestHeartbeatPayload_SetsTraceID covers the LIVE worker heartbeat path
// (runtime.HeartbeatPayload* -> CAP capworker.HeartbeatPayloadWithProgress, which
// hardcodes TraceId:""). CAP v2.x ValidateBusPacket — the same required-field contract
// the scheduler/gateway bus enforces — rejects an empty trace_id, so without a stamp every
// sys.heartbeat is dropped as "buspacket: missing required field(s): trace_id". This asserts
// the wrapper stamps trace_id == workerID across all three constructors, with and without an
// option, and leaves the heartbeat payload intact.
func TestHeartbeatPayload_SetsTraceID(t *testing.T) {
	const (
		workerID  = "hb-worker-1"
		pool      = "hb-pool"
		authToken = "attestation-xyz"
	)

	builders := []struct {
		name  string
		build func(opts ...HeartbeatOption) ([]byte, error)
	}{
		{
			name: "HeartbeatPayload",
			build: func(opts ...HeartbeatOption) ([]byte, error) {
				return HeartbeatPayload(workerID, pool, 2, 8, 12.5, opts...)
			},
		},
		{
			name: "HeartbeatPayloadWithMemory",
			build: func(opts ...HeartbeatOption) ([]byte, error) {
				return HeartbeatPayloadWithMemory(workerID, pool, 2, 8, 12.5, 33.0, opts...)
			},
		},
		{
			name: "HeartbeatPayloadWithProgress",
			build: func(opts ...HeartbeatOption) ([]byte, error) {
				return HeartbeatPayloadWithProgress(workerID, pool, 2, 8, 12.5, 33.0, 50, "memo-1", opts...)
			},
		},
	}

	for _, b := range builders {
		for _, withOpt := range []bool{false, true} {
			name := b.name + "/noOption"
			if withOpt {
				name = b.name + "/withOption"
			}
			t.Run(name, func(t *testing.T) {
				var opts []HeartbeatOption
				if withOpt {
					opts = append(opts, WithAuthToken(authToken))
				}

				data, err := b.build(opts...)
				if err != nil {
					t.Fatalf("build heartbeat: %v", err)
				}

				var pkt agentv1.BusPacket
				if err := proto.Unmarshal(data, &pkt); err != nil {
					t.Fatalf("unmarshal packet: %v", err)
				}

				// (a) The bus rejects an empty trace_id; this is the exact contract it enforces.
				if err := capsdk.ValidateBusPacket(&pkt); err != nil {
					t.Fatalf("ValidateBusPacket rejected heartbeat: %v", err)
				}
				// (b) trace_id is stamped with the workerID correlation id.
				if got := pkt.GetTraceId(); got != workerID {
					t.Fatalf("trace_id = %q, want %q", got, workerID)
				}
				// (c) the heartbeat payload is intact and options are applied only when supplied.
				hb := pkt.GetHeartbeat()
				if hb == nil {
					t.Fatal("expected heartbeat payload")
				}
				if hb.GetWorkerId() != workerID {
					t.Fatalf("heartbeat worker_id = %q, want %q", hb.GetWorkerId(), workerID)
				}
				if hb.GetPool() != pool {
					t.Fatalf("heartbeat pool = %q, want %q", hb.GetPool(), pool)
				}
				if withOpt {
					if hb.GetAuthToken() != authToken {
						t.Fatalf("auth_token = %q, want %q (option not applied)", hb.GetAuthToken(), authToken)
					}
				} else if hb.GetAuthToken() != "" {
					t.Fatalf("auth_token = %q, want empty (no option supplied)", hb.GetAuthToken())
				}
			})
		}
	}
}
