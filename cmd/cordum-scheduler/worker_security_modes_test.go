package main

import (
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
)

func TestValidateWorkerSecurityModesRejectsCompetingTokenSchemes(t *testing.T) {
	for _, handshake := range []scheduler.HandshakeMode{
		scheduler.HandshakeModeWarn,
		scheduler.HandshakeModeEnforce,
	} {
		for _, attestation := range []scheduler.WorkerAttestationMode{
			scheduler.WorkerAttestationWarn,
			scheduler.WorkerAttestationEnforce,
		} {
			if err := validateWorkerSecurityModes(handshake, attestation); err == nil {
				t.Fatalf("handshake=%s attestation=%s accepted competing auth_token schemes", handshake, attestation)
			}
		}
	}
}

func TestValidateWorkerSecurityModesAcceptsSingleAuthority(t *testing.T) {
	cases := []struct {
		handshake   scheduler.HandshakeMode
		attestation scheduler.WorkerAttestationMode
	}{
		{scheduler.HandshakeModeOff, scheduler.WorkerAttestationOff},
		{scheduler.HandshakeModeOff, scheduler.WorkerAttestationWarn},
		{scheduler.HandshakeModeOff, scheduler.WorkerAttestationEnforce},
		{scheduler.HandshakeModeWarn, scheduler.WorkerAttestationOff},
		{scheduler.HandshakeModeEnforce, scheduler.WorkerAttestationOff},
	}
	for _, testCase := range cases {
		if err := validateWorkerSecurityModes(testCase.handshake, testCase.attestation); err != nil {
			t.Fatalf("handshake=%s attestation=%s rejected: %v", testCase.handshake, testCase.attestation, err)
		}
	}
}

func TestValidateWorkerSecurityModesRejectsUnknownValues(t *testing.T) {
	if err := validateWorkerSecurityModes(scheduler.HandshakeMode("typo"), scheduler.WorkerAttestationOff); err == nil {
		t.Fatal("unknown handshake mode accepted")
	}
	if err := validateWorkerSecurityModes(scheduler.HandshakeModeOff, scheduler.WorkerAttestationMode("typo")); err == nil {
		t.Fatal("unknown attestation mode accepted")
	}
}
