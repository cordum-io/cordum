package bus

import (
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

func startTestNATS(t *testing.T) *server.Server {
	t.Helper()
	opts := &server.Options{Port: -1, NoLog: true, NoSigs: true}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("start test nats: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns
}

func TestPublishHandshake(t *testing.T) {
	ns := startTestNATS(t)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()

	b := &NatsBus{nc: nc}

	// Subscribe to handshake subject before publishing.
	sub, err := nc.SubscribeSync(capsdk.SubjectHandshake)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	caps := map[string]bool{"http": true, "grpc": true}
	if err := PublishHandshake(b, "test-gateway", pb.ComponentRole_COMPONENT_ROLE_GATEWAY, caps); err != nil {
		t.Fatalf("publish handshake: %v", err)
	}
	nc.Flush()

	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("receive handshake: %v", err)
	}

	var packet pb.BusPacket
	if err := proto.Unmarshal(msg.Data, &packet); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	hs := packet.GetHandshake()
	if hs == nil {
		t.Fatal("expected handshake payload")
	}
	if hs.ComponentId != "test-gateway" {
		t.Fatalf("component_id = %q, want %q", hs.ComponentId, "test-gateway")
	}
	if hs.Role != pb.ComponentRole_COMPONENT_ROLE_GATEWAY {
		t.Fatalf("role = %v, want GATEWAY", hs.Role)
	}
	if !hs.Capabilities["http"] || !hs.Capabilities["grpc"] {
		t.Fatalf("capabilities = %v, want http+grpc", hs.Capabilities)
	}
	if len(hs.SupportedVersions) != 1 || hs.SupportedVersions[0] != int32(capsdk.DefaultProtocolVersion) {
		t.Fatalf("supported_versions = %v, want [%d]", hs.SupportedVersions, capsdk.DefaultProtocolVersion)
	}
	if packet.SenderId != "test-gateway" {
		t.Fatalf("sender_id = %q, want %q", packet.SenderId, "test-gateway")
	}
	if packet.TraceId == "" {
		t.Fatal("expected non-empty trace_id")
	}
	if packet.ProtocolVersion != capsdk.DefaultProtocolVersion {
		t.Fatalf("protocol_version = %d, want %d", packet.ProtocolVersion, capsdk.DefaultProtocolVersion)
	}
}

func TestPublishHandshakeNilBus(t *testing.T) {
	if err := PublishHandshake(nil, "x", pb.ComponentRole_COMPONENT_ROLE_GATEWAY, nil); err == nil {
		t.Fatal("expected error for nil bus")
	}
}

func TestPublishHandshakeEmptyComponentID(t *testing.T) {
	ns := startTestNATS(t)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()

	b := &NatsBus{nc: nc}
	if err := PublishHandshake(b, "", pb.ComponentRole_COMPONENT_ROLE_GATEWAY, nil); err == nil {
		t.Fatal("expected error for empty component ID")
	}
}
