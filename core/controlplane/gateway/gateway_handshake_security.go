package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type serviceTokenMinter interface {
	MintServiceToken(subject string) (string, error)
}

type gatewayHandshakeSecurity struct {
	mode          scheduler.HandshakeMode
	heartbeatMode scheduler.HeartbeatMode
	issuer        *scheduler.SessionTokenIssuer
}

func loadGatewayHandshakeSecurity(rdb redis.UniversalClient) (gatewayHandshakeSecurity, error) {
	mode, err := scheduler.ParseHandshakeModeStrict(os.Getenv(scheduler.EnvHandshakeMode))
	if err != nil {
		return gatewayHandshakeSecurity{}, err
	}
	heartbeatMode, err := scheduler.ParseHeartbeatModeStrict(os.Getenv(scheduler.EnvHeartbeatMode))
	if err != nil {
		return gatewayHandshakeSecurity{}, err
	}
	handshakeActive := mode != scheduler.HandshakeModeOff
	if handshakeActive != heartbeatMode.EnforcesSession() {
		return gatewayHandshakeSecurity{}, errors.New("gateway: handshake and heartbeat authority modes must transition together")
	}
	security := gatewayHandshakeSecurity{mode: mode, heartbeatMode: heartbeatMode}
	if mode == scheduler.HandshakeModeOff {
		return security, nil
	}
	if rdb == nil {
		return gatewayHandshakeSecurity{}, errors.New("gateway handshake security requires Redis session authority")
	}
	privateKey, keyID, err := policysign.LoadPrivateKeyFromEnv()
	if err != nil {
		return gatewayHandshakeSecurity{}, fmt.Errorf("load gateway session signing key: %w", err)
	}
	trust, err := policysign.LoadTrustStoreFromEnv()
	if err != nil {
		return gatewayHandshakeSecurity{}, fmt.Errorf("load gateway session trust store: %w", err)
	}
	security.issuer, err = scheduler.NewSessionTokenIssuer(
		privateKey, keyID, trust, rdb, scheduler.SessionTokenIssuerOptions{},
	)
	if err != nil {
		return gatewayHandshakeSecurity{}, fmt.Errorf("construct gateway session issuer: %w", err)
	}
	return security, nil
}

func (s *server) handshakeSecurityActive() bool {
	if s == nil {
		return false
	}
	return s.handshakeMode != "" && s.handshakeMode != scheduler.HandshakeModeOff
}

func (s *server) authorizeConfigChanged(packet *pb.BusPacket) bool {
	if !s.handshakeSecurityActive() {
		return true
	}
	if s.serviceTokenMinter == nil {
		slog.Error("config change notification rejected: gateway service-token issuer unavailable")
		return false
	}
	token, err := s.serviceTokenMinter.MintServiceToken(servicetoken.IdentityGateway)
	if err != nil {
		slog.Error("config change notification rejected: gateway service-token mint failed", "error", err)
		return false
	}
	if strings.TrimSpace(token) == "" {
		slog.Error("config change notification rejected: gateway service-token minter returned an empty token")
		return false
	}
	packet.AuthToken = token
	return true
}
