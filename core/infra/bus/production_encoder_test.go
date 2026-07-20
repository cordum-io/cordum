package bus

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	capv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	production "github.com/cordum-io/cap/v2/sdk/go"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCompatibilityPacketEncoderIsExplicitProtoMarshal(t *testing.T) {
	packet := productionEncoderPacket()
	want, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	b := &NatsBus{}
	got, err := b.encodePacket(context.Background(), "sys.job.result", packet)
	if err != nil {
		t.Fatalf("encodePacket: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("compatibility wire differs from proto.Marshal")
	}
}

func TestProductionPacketEncoderSignsActualSubjectWithUniqueMessageIDs(t *testing.T) {
	key := productionEncoderKey(t)
	now := time.Now().UTC()
	encoder, err := NewProductionPacketEncoder(ProductionPacketEncoderOptions{
		Key: key, KeyID: "worker-key-1", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewProductionPacketEncoder: %v", err)
	}
	packet := productionEncoderPacket()
	first := decodeEncodedProductionPacket(t, encoder, "sys.job.result", packet)
	second := decodeEncodedProductionPacket(t, encoder, "sys.job.result", packet)
	if bytes.Equal(first.GetSignatureMetadata().GetMessageId(), second.GetSignatureMetadata().GetMessageId()) {
		t.Fatal("two publishes reused a production message ID")
	}
	if packet.GetSignatureMetadata() != nil || len(packet.GetSignature()) != 0 {
		t.Fatal("production encoder mutated caller-owned packet")
	}
	trust := production.ProductionTrustStore{
		Audience: "sys.job.result", Tenant: "tenant-a", Sender: "worker-1",
		PublicKeys: map[string]*ecdsa.PublicKey{"worker-key-1": &key.PublicKey},
		Now:        func() time.Time { return now },
	}
	raw, err := encoder(context.Background(), "sys.job.result", packet)
	if err != nil {
		t.Fatalf("encode for verify: %v", err)
	}
	if _, err := production.VerifyProductionPacket(raw, trust); err != nil {
		t.Fatalf("VerifyProductionPacket(actual subject): %v", err)
	}
	trust.Audience = "sys.job.progress"
	if _, err := production.VerifyProductionPacket(raw, trust); !errors.Is(err, production.ErrAudienceMismatch) {
		t.Fatalf("wrong-subject verification error = %v, want ErrAudienceMismatch", err)
	}
}

func TestNatsBusPublishEmitsConfiguredProductionWire(t *testing.T) {
	ns := startTestNATSServer(t, false)
	b := newTestNatsBus(t, ns, false)
	key := productionEncoderKey(t)
	encoder, err := NewProductionPacketEncoder(ProductionPacketEncoderOptions{Key: key, KeyID: "worker-key-1"})
	if err != nil {
		t.Fatalf("NewProductionPacketEncoder: %v", err)
	}
	if err := b.SetPacketEncoder(encoder); err != nil {
		t.Fatalf("SetPacketEncoder: %v", err)
	}
	subject := "sys.job.result"
	received := make(chan []byte, 1)
	sub, err := b.nc.Subscribe(subject, func(msg *nats.Msg) { received <- append([]byte(nil), msg.Data...) })
	if err != nil {
		t.Fatalf("raw subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := b.nc.Flush(); err != nil {
		t.Fatalf("flush subscribe: %v", err)
	}
	if err := b.Publish(subject, productionEncoderPacket()); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case raw := <-received:
		trust := production.ProductionTrustStore{
			Audience: subject, Tenant: "tenant-a", Sender: "worker-1",
			PublicKeys: map[string]*ecdsa.PublicKey{"worker-key-1": &key.PublicKey},
		}
		if _, err := production.VerifyProductionPacket(raw, trust); err != nil {
			t.Fatalf("published wire verification: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published production wire")
	}
}

func productionEncoderKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func productionEncoderPacket() *pb.BusPacket {
	identity := &capv1.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a"}
	return &pb.BusPacket{
		ProtocolVersion: 1, SenderId: "worker-1", CreatedAt: timestamppb.Now(), Identity: identity,
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: "job-1", WorkerId: "worker-1", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, Identity: identity,
		}},
	}
}

func decodeEncodedProductionPacket(t *testing.T, encoder PacketEncoder, subject string, packet *pb.BusPacket) *pb.BusPacket {
	t.Helper()
	raw, err := encoder(context.Background(), subject, packet)
	if err != nil {
		t.Fatalf("encode production packet: %v", err)
	}
	decoded := &pb.BusPacket{}
	if err := proto.Unmarshal(raw, decoded); err != nil {
		t.Fatalf("unmarshal production wire: %v", err)
	}
	return decoded
}
