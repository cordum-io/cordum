package bus

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	production "github.com/cordum-io/cap/v2/sdk/go"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PacketEncoder converts an outbound packet to the exact bytes published on
// the supplied transport subject.
type PacketEncoder func(context.Context, string, *pb.BusPacket) ([]byte, error)

// ProductionPacketEncoderOptions configure CAP-PRODUCTION wire signing.
type ProductionPacketEncoderOptions struct {
	Key      *ecdsa.PrivateKey
	KeyID    string
	Now      func() time.Time
	Lifetime time.Duration
}

var (
	ErrPacketEncoderFrozen = errors.New("packet encoder is frozen")
	ErrPacketAlreadySigned = errors.New("packet already contains signature metadata")
)

// SetPacketEncoder installs an encoder before the first subscription starts.
// A nil encoder explicitly selects compatibility protobuf marshaling.
func (b *NatsBus) SetPacketEncoder(encoder PacketEncoder) error {
	if b == nil {
		return errNilBus
	}
	b.hooksMu.Lock()
	defer b.hooksMu.Unlock()
	if b.rawAdmissionFrozen {
		return ErrPacketEncoderFrozen
	}
	b.packetEncoder = encoder
	return nil
}

func (b *NatsBus) encodePacket(ctx context.Context, subject string, packet *pb.BusPacket) ([]byte, error) {
	b.hooksMu.RLock()
	encoder := b.packetEncoder
	b.hooksMu.RUnlock()
	if encoder == nil {
		return proto.Marshal(packet)
	}
	return encoder(ctx, subject, packet)
}

// NewProductionPacketEncoder returns an encoder that signs the actual NATS
// subject and creates a fresh cryptographic message ID for every publish.
func NewProductionPacketEncoder(options ProductionPacketEncoderOptions) (PacketEncoder, error) {
	if options.Key == nil || options.Key.Curve != elliptic.P256() || strings.TrimSpace(options.KeyID) == "" {
		return nil, errors.New("invalid production signing key configuration")
	}
	lifetime := options.Lifetime
	if lifetime == 0 {
		lifetime = production.DefaultProductionMaxLifetime
	}
	if lifetime < 0 || lifetime > production.DefaultProductionMaxLifetime {
		return nil, errors.New("invalid production signature lifetime")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	keyID := strings.TrimSpace(options.KeyID)
	return func(ctx context.Context, subject string, packet *pb.BusPacket) ([]byte, error) {
		return encodeProductionPacket(ctx, subject, packet, options.Key, keyID, now(), lifetime)
	}, nil
}

func encodeProductionPacket(
	ctx context.Context, subject string, packet *pb.BusPacket,
	key *ecdsa.PrivateKey, keyID string, now time.Time, lifetime time.Duration,
) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, context.Canceled
	}
	if packet == nil || strings.TrimSpace(subject) == "" {
		return nil, errors.New("invalid production packet")
	}
	if packet.GetSignatureMetadata() != nil || len(packet.GetSignature()) != 0 {
		return nil, ErrPacketAlreadySigned
	}
	messageID := make([]byte, 16)
	if _, err := rand.Read(messageID); err != nil {
		return nil, fmt.Errorf("generate production message id: %w", err)
	}
	cloned := proto.Clone(packet).(*pb.BusPacket)
	cloned.SignatureMetadata = &pb.SignatureMetadata{
		ProfileVersion: production.ProductionProfileVersion,
		Algorithm:      production.ProductionAlgorithm, MessageId: messageID,
		Audience: subject, ExpiresAt: timestamppb.New(now.Add(lifetime)), KeyId: keyID,
	}
	return production.SignProductionPacket(cloned, key)
}
