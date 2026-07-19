package main

import (
	"fmt"

	"github.com/cordum/cordum/core/controlplane/scheduler"
)

// validateWorkerSecurityModes prevents one auth_token field from being
// interpreted as both a bound session token and a legacy bearer credential.
func validateWorkerSecurityModes(
	handshake scheduler.HandshakeMode,
	attestation scheduler.WorkerAttestationMode,
) error {
	switch handshake {
	case scheduler.HandshakeModeOff, scheduler.HandshakeModeWarn, scheduler.HandshakeModeEnforce:
	default:
		return fmt.Errorf("invalid worker handshake mode %q", handshake)
	}
	switch attestation {
	case scheduler.WorkerAttestationOff, scheduler.WorkerAttestationWarn, scheduler.WorkerAttestationEnforce:
	default:
		return fmt.Errorf("invalid worker attestation mode %q", attestation)
	}
	if handshake != scheduler.HandshakeModeOff && attestation != scheduler.WorkerAttestationOff {
		return fmt.Errorf("%s and %s cannot both be active: they use incompatible auth_token schemes",
			scheduler.EnvHandshakeMode, scheduler.EnvWorkerAttestation)
	}
	return nil
}
