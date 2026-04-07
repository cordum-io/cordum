package runtime

import (
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
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
