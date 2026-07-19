//go:build handshakeinterop

package handshakeinterop

import (
	"fmt"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *interopServer) awaitReplicaReady(replica *countedService) {
	s.t.Helper()
	if replica == nil {
		s.t.Fatal("readiness replica required")
	}
	connection, err := nats.Connect(s.natsURL(), nats.Timeout(2*time.Second))
	if err != nil {
		s.t.Fatalf("connect readiness probe: %v", err)
	}
	defer connection.Close()
	baseline := replica.calls()
	deadline := time.NewTimer(5 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		if err := publishReadinessProbe(connection, s.runID); err != nil {
			s.t.Fatalf("publish readiness probe: %v", err)
		}
		select {
		case <-replica.called:
			if replica.calls() > baseline {
				return
			}
		case <-ticker.C:
		case <-deadline.C:
			s.t.Fatal("handshake subscriber did not become ready")
		}
	}
}

func publishReadinessProbe(connection *nats.Conn, runID string) error {
	packet := &agentv1.BusPacket{
		ProtocolVersion: 1, TraceId: "readiness-" + runID,
		SenderId: "invalid-readiness-probe", CreatedAt: timestamppb.Now(),
	}
	wire, err := proto.Marshal(packet)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := connection.PublishRequest(capsdk.WorkerHandshakeChallengeSubject, nats.NewInbox(), wire); err != nil {
		return err
	}
	return connection.FlushTimeout(time.Second)
}

func (s *interopServer) awaitActiveReplicasReady() {
	s.t.Helper()
	if len(s.buses) > len(s.replicas) {
		s.t.Fatal("active replica accounting invalid")
	}
	active := s.replicas[len(s.replicas)-len(s.buses):]
	for _, replica := range active {
		s.awaitReplicaReady(replica)
	}
}

func TestReadinessProbeHasNoTrustPayload(t *testing.T) {
	packet := &agentv1.BusPacket{
		ProtocolVersion: 1, TraceId: "readiness-test", SenderId: "invalid-readiness-probe",
		CreatedAt: timestamppb.Now(),
	}
	if packet.GetWorkerHandshakeChallengeRequest() != nil || packet.GetWorkerHandshakeAuthenticate() != nil {
		t.Fatal("readiness probe must not create handshake authority")
	}
}
