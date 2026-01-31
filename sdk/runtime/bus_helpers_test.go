package runtime

import (
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
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

func TestPublishProgress(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pub := &capturePublisher{}
		progress := &agentv1.JobProgress{
			JobId:   "job-1",
			Percent: 50,
			Message: "halfway",
		}

		if err := PublishProgress(pub, progress, "", "worker-1", nil); err != nil {
			t.Fatalf("PublishProgress failed: %v", err)
		}
		if pub.subject != SubjectProgress {
			t.Fatalf("expected subject %q, got %q", SubjectProgress, pub.subject)
		}

		var pkt agentv1.BusPacket
		if err := proto.Unmarshal(pub.data, &pkt); err != nil {
			t.Fatalf("unmarshal packet: %v", err)
		}
		if pkt.GetTraceId() != "job-1" {
			t.Fatalf("expected trace id job-1, got %q", pkt.GetTraceId())
		}
		if pkt.GetSenderId() != "worker-1" {
			t.Fatalf("expected sender id worker-1, got %q", pkt.GetSenderId())
		}
		if pkt.GetCreatedAt() == nil {
			t.Fatalf("expected created_at to be set")
		}
		progressMsg := pkt.GetJobProgress()
		if progressMsg == nil {
			t.Fatalf("expected job progress payload")
		}
		if progressMsg.GetJobId() != "job-1" || progressMsg.GetPercent() != 50 {
			t.Fatalf("unexpected progress payload: %#v", progressMsg)
		}
	})

	t.Run("validation", func(t *testing.T) {
		if err := PublishProgress(nil, &agentv1.JobProgress{JobId: "job-1"}, "trace", "worker-1", nil); err == nil {
			t.Fatalf("expected error for nil publisher")
		}
		if err := PublishProgress(&capturePublisher{}, nil, "trace", "worker-1", nil); err == nil {
			t.Fatalf("expected error for nil progress")
		}
		if err := PublishProgress(&capturePublisher{}, &agentv1.JobProgress{}, "trace", "worker-1", nil); err == nil {
			t.Fatalf("expected error for empty job id")
		}
		if err := PublishProgress(&capturePublisher{}, &agentv1.JobProgress{JobId: "job-1"}, "trace", "", nil); err == nil {
			t.Fatalf("expected error for empty sender id")
		}
	})
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
