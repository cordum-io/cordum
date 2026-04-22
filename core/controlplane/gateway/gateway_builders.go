package gateway

import "github.com/cordum/cordum/core/controlplane/scheduler"

// WithSessionIssuer wires the worker-session issuer used by the revoke/session
// handlers. Returns the same server so bootstrap code can chain builders.
func (s *server) WithSessionIssuer(issuer *scheduler.SessionTokenIssuer) *server {
	if s == nil {
		return nil
	}
	s.sessionIssuer = issuer
	return s
}

// WithTrustResolver wires the authoritative worker-trust resolver.
func (s *server) WithTrustResolver(resolver *scheduler.TrustResolver) *server {
	if s == nil {
		return nil
	}
	s.trustResolver = resolver
	return s
}

// WithHeartbeatMode records the rollout mode used by worker-session surfaces.
func (s *server) WithHeartbeatMode(mode scheduler.HeartbeatMode) *server {
	if s == nil {
		return nil
	}
	s.heartbeatMode = mode
	return s
}
