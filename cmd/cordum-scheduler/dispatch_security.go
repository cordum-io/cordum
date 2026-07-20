package main

import (
	"errors"
	"fmt"

	"github.com/cordum/cordum/core/controlplane/scheduler"
)

func newProductionDispatchGateForModes(
	handshakeMode scheduler.HandshakeMode,
	heartbeatMode scheduler.HeartbeatMode,
	resolver *scheduler.TrustResolver,
) (*scheduler.DispatchGate, error) {
	handshakeActive, err := activeHandshakeMode(handshakeMode)
	if err != nil {
		return nil, err
	}
	if !knownHeartbeatMode(heartbeatMode) {
		return nil, fmt.Errorf("scheduler: invalid heartbeat mode %q", heartbeatMode)
	}
	if handshakeActive != heartbeatMode.EnforcesSession() {
		return nil, errors.New("scheduler: handshake and heartbeat authority modes must transition together")
	}
	return newProductionDispatchGate(heartbeatMode, resolver)
}

func activeHandshakeMode(mode scheduler.HandshakeMode) (bool, error) {
	switch mode {
	case scheduler.HandshakeModeOff:
		return false, nil
	case scheduler.HandshakeModeWarn, scheduler.HandshakeModeEnforce:
		return true, nil
	default:
		return false, fmt.Errorf("scheduler: invalid handshake mode %q", mode)
	}
}

func knownHeartbeatMode(mode scheduler.HeartbeatMode) bool {
	switch mode {
	case scheduler.HeartbeatModeAuthority, scheduler.HeartbeatModeWarn, scheduler.HeartbeatModeTelemetry:
		return true
	default:
		return false
	}
}

func newProductionDispatchGate(mode scheduler.HeartbeatMode, resolver *scheduler.TrustResolver) (*scheduler.DispatchGate, error) {
	if mode.EnforcesSession() && (resolver == nil || !resolver.BoundAuthorityReady()) {
		return nil, errors.New("scheduler: session-authority dispatch requires bound trust resolver")
	}
	return scheduler.NewDispatchGate(resolver, mode), nil
}
