package bus

import (
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

// NatsBus is a thin wrapper over a NATS connection that speaks protobuf packets.
type NatsBus struct {
	nc *nats.Conn
}

// NewNatsBus dials NATS at the provided URL.
func NewNatsBus(url string) (*NatsBus, error) {
	opts := []nats.Option{
		nats.Name("coretex-bus"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Printf("[BUS] disconnected from NATS: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("[BUS] reconnected to NATS at %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			log.Printf("[BUS] connection closed")
		}),
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, err
	}
	return &NatsBus{nc: nc}, nil
}

// Close shuts down the underlying NATS connection.
func (b *NatsBus) Close() {
	if b.nc != nil {
		b.nc.Close()
	}
}

// DirectSubject constructs a worker-specific subject for targeted delivery.
func DirectSubject(workerID string) string {
	if workerID == "" {
		return ""
	}
	return fmt.Sprintf("worker.%s.jobs", workerID)
}

// Publish sends a protobuf-encoded BusPacket on the given subject.
func (b *NatsBus) Publish(subject string, packet *pb.BusPacket) error {
	data, err := proto.Marshal(packet)
	if err != nil {
		return err
	}
	return b.nc.Publish(subject, data)
}

// Subscribe attaches a queue subscription that decodes protobuf packets and invokes the handler.
func (b *NatsBus) Subscribe(subject, queue string, handler func(*pb.BusPacket)) error {
	cb := func(msg *nats.Msg) {
		var packet pb.BusPacket
		if err := proto.Unmarshal(msg.Data, &packet); err != nil {
			log.Printf("nats bus: failed to unmarshal packet: %v", err)
			return
		}
		handler(&packet)
	}
	if queue == "" {
		_, err := b.nc.Subscribe(subject, cb)
		return err
	}
	_, err := b.nc.QueueSubscribe(subject, queue, cb)
	return err
}

func (b *NatsBus) IsConnected() bool {
	return b != nil && b.nc != nil && b.nc.IsConnected()
}

func (b *NatsBus) Status() string {
	if b == nil || b.nc == nil {
		return "UNKNOWN"
	}
	return b.nc.Status().String()
}

func (b *NatsBus) ConnectedURL() string {
	if b == nil || b.nc == nil {
		return ""
	}
	return b.nc.ConnectedUrl()
}
