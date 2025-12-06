package bus

import (
	"log"
	"time"

	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// NatsBus is a thin wrapper over a NATS connection that speaks protobuf packets.
type NatsBus struct {
	nc *nats.Conn
}

// NewNatsBus dials NATS at the provided URL.
func NewNatsBus(url string) (*NatsBus, error) {
	opts := []nats.Option{
		nats.Name("cortex-bus"),
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
	_, err := b.nc.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		var packet pb.BusPacket
		if err := proto.Unmarshal(msg.Data, &packet); err != nil {
			log.Printf("nats bus: failed to unmarshal packet: %v", err)
			return
		}
		handler(&packet)
	})
	return err
}
