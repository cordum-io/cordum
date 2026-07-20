package main

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
)

type bootCredentialResolver struct{}

func (bootCredentialResolver) GetByWorkerID(context.Context, string) (*workercredentials.Credential, error) {
	return nil, nil
}

func TestNewProductionDispatchGateForModesRejectsPassThroughMismatch(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	resolver, err := scheduler.NewBoundTrustResolver(client, bootCredentialResolver{})
	if err != nil {
		t.Fatalf("NewBoundTrustResolver: %v", err)
	}

	tests := []struct {
		name      string
		handshake scheduler.HandshakeMode
		heartbeat scheduler.HeartbeatMode
		wantErr   string
		enforces  bool
	}{
		{"off with authority", scheduler.HandshakeModeOff, scheduler.HeartbeatModeAuthority, "", false},
		{"warn with warn", scheduler.HandshakeModeWarn, scheduler.HeartbeatModeWarn, "", true},
		{"enforce with telemetry", scheduler.HandshakeModeEnforce, scheduler.HeartbeatModeTelemetry, "", true},
		{"off with warn", scheduler.HandshakeModeOff, scheduler.HeartbeatModeWarn, "must transition together", false},
		{"off with telemetry", scheduler.HandshakeModeOff, scheduler.HeartbeatModeTelemetry, "must transition together", false},
		{"warn with authority", scheduler.HandshakeModeWarn, scheduler.HeartbeatModeAuthority, "must transition together", false},
		{"enforce with authority", scheduler.HandshakeModeEnforce, scheduler.HeartbeatModeAuthority, "must transition together", false},
		{"invalid handshake", scheduler.HandshakeMode("typo"), scheduler.HeartbeatModeAuthority, "invalid handshake mode", false},
		{"invalid heartbeat", scheduler.HandshakeModeOff, scheduler.HeartbeatMode("typo"), "invalid heartbeat mode", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate, err := newProductionDispatchGateForModes(test.handshake, test.heartbeat, resolver)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, test.wantErr)
				}
				if gate != nil {
					t.Fatalf("incompatible modes returned gate %#v", gate)
				}
				return
			}
			if err != nil || gate == nil {
				t.Fatalf("compatible modes rejected: gate=%#v err=%v", gate, err)
			}
			if gate.EnforcesSession() != test.enforces {
				t.Fatalf("EnforcesSession = %v, want %v", gate.EnforcesSession(), test.enforces)
			}
		})
	}
}
