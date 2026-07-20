package scheduler

import (
	"context"
	"errors"
	"strings"

	"github.com/cordum/cordum/core/auth/servicetoken"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

var ErrProductionSessionUnavailable = errors.New("scheduler: production session authority unavailable")

// NewProductionSessionResolver verifies the signed session/service token
// before returning authority. Packet identity is copied only as a signed
// assertion; worker tenant and sender are bound to token claims here.
func NewProductionSessionResolver(middleware *SessionTokenMiddleware) (ProductionSessionResolver, error) {
	if middleware == nil || middleware.issuer == nil || middleware.mode != HandshakeModeEnforce {
		return nil, ErrProductionSessionUnavailable
	}
	return func(ctx context.Context, actualSubject string, raw []byte) (AuthenticatedProductionSession, error) {
		if ctx == nil || strings.TrimSpace(actualSubject) == "" || len(raw) == 0 || len(raw) > defaultProductionMaxRawBytes {
			return AuthenticatedProductionSession{}, ErrProductionSessionUnavailable
		}
		packet := &pb.BusPacket{}
		if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, packet); err != nil {
			return AuthenticatedProductionSession{}, ErrProductionSessionUnavailable
		}
		result := middleware.Verify(ctx, packet.GetSenderId(), packet)
		if result.Verdict != TokenVerdictPass || result.Claims == nil {
			return AuthenticatedProductionSession{}, ErrProductionSessionUnavailable
		}
		return productionSessionFromClaims(packet, *result.Claims)
	}, nil
}

func productionSessionFromClaims(
	packet *pb.BusPacket,
	claims SessionTokenClaims,
) (AuthenticatedProductionSession, error) {
	identity := packet.GetIdentity()
	if packet.GetSenderId() != claims.Subject || !completeProductionIdentity(identity) {
		return AuthenticatedProductionSession{}, ErrProductionSessionUnavailable
	}
	if !servicetoken.IsReservedIdentity(claims.Subject) {
		if err := claims.validateBound(); err != nil || claims.Audience != WorkerHandshakeAudience ||
			identity.GetTenantId() != claims.Tenant {
			return AuthenticatedProductionSession{}, ErrProductionSessionUnavailable
		}
	}
	return AuthenticatedProductionSession{
		Subject: claims.Subject, Tenant: claims.Tenant,
		Identity: proto.Clone(identity).(*pb.IdentityBinding),
	}, nil
}
