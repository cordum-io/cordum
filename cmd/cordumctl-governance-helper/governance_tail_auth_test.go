package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGovernanceTailRecordsOnlyVerifiedPublishers(t *testing.T) {
	clearGovernanceTrust(t)
	cp := governanceTrustKey(t, "CORDUM_POLICY_PUBLIC_KEY_cp")
	kernel := governanceTrustKey(t, "CORDUM_AUDIT_PUBLIC_KEY_CORDUM_SAFETY_KERNEL")
	other := governanceTrustKey(t, "")
	cases := []struct {
		name  string
		token string
		keys  int
	}{
		{"no-token", "", 0},
		{"untrusted-key", governanceServiceToken(t, other, servicetoken.IdentityGateway), 0},
		{"control-plane-subject", governanceServiceToken(t, cp, servicetoken.IdentityWorkflow), 0},
		{"publisher-subject", governanceServiceToken(t, kernel, servicetoken.IdentityMCP), 0},
		{"gateway", governanceServiceToken(t, cp, servicetoken.IdentityGateway), 3},
		{"kernel", governanceServiceToken(t, kernel, servicetoken.IdentitySafetyKernel), 3},
	}
	var packets [][]byte
	for _, tc := range cases {
		packets = append(packets, governanceAuthenticatedPacket(t, tc.name, tc.token))
	}
	h, summary, progress := runGovernanceAuthPackets(t, packets)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := h.runtime.rawClient.Keys(context.Background(), "gov:dec:"+tc.name+":*").Result()
			if err != nil || len(keys) != tc.keys {
				t.Errorf("decision keys = %d, err = %v; want %d", len(keys), err, tc.keys)
			}
		})
	}
	keys, err := h.runtime.rawClient.Keys(context.Background(), "gov:dec:*").Result()
	if err != nil || len(keys) != 6 {
		t.Errorf("total decision keys = %d, err = %v; want 6", len(keys), err)
	}
	assertGovernanceRefusals(t, summary, progress, 4)
}

func TestGovernanceTailTrustLoadErrors(t *testing.T) {
	for _, variable := range []string{"CORDUM_POLICY_PUBLIC_KEY_cp", "CORDUM_AUDIT_PUBLIC_KEY_CORDUM_SAFETY_KERNEL"} {
		t.Run(variable, func(t *testing.T) {
			clearGovernanceTrust(t)
			t.Setenv(variable, "invalid-key")
			called := false
			subscriber := governanceSubscriberFunc(func(func([]byte) error) error { called = true; return nil })
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := runGovernanceTail(ctx, subscriber, nil, nil)
			if err == nil || !strings.Contains(err.Error(), variable) || called {
				t.Fatalf("startup err = %v, subscribed = %v", err, called)
			}
		})
	}
}

func TestGovernanceTailEmptyTrust(t *testing.T) {
	clearGovernanceTrust(t)
	token := governanceServiceToken(t, governanceTrustKey(t, ""), servicetoken.IdentityGateway)
	h, summary, progress := runGovernanceAuthPackets(t, [][]byte{governanceAuthenticatedPacket(t, "empty-trust", token)})
	keys, err := h.runtime.rawClient.Keys(context.Background(), "gov:dec:*").Result()
	if err != nil || len(keys) != 0 {
		t.Fatalf("decision keys = %d, err = %v; want 0", len(keys), err)
	}
	assertGovernanceRefusals(t, summary, progress, 1)
}

func TestGovernanceTailUsesReceiveTime(t *testing.T) {
	clearGovernanceTrust(t)
	key := governanceTrustKey(t, "CORDUM_POLICY_PUBLIC_KEY_cp")
	issued := time.Now().Add(-time.Hour)
	token, err := servicetoken.MintService(key, "cp", servicetoken.IdentityGateway, issued)
	if err != nil {
		t.Fatal(err)
	}
	var packet pb.BusPacket
	if err := proto.Unmarshal(governanceAuthenticatedPacket(t, "expired", token), &packet); err != nil {
		t.Fatal(err)
	}
	packet.CreatedAt = timestamppb.New(issued)
	data, err := proto.Marshal(&packet)
	if err != nil {
		t.Fatal(err)
	}
	h, summary, progress := runGovernanceAuthPackets(t, [][]byte{data})
	keys, err := h.runtime.rawClient.Keys(context.Background(), "gov:dec:*").Result()
	if err != nil || len(keys) != 0 {
		t.Fatalf("decision keys = %d, err = %v; want 0", len(keys), err)
	}
	assertGovernanceRefusals(t, summary, progress, 1)
}

func TestGovernanceTailChecksBeforeEventDecode(t *testing.T) {
	clearGovernanceTrust(t)
	packet := &pb.BusPacket{SenderId: "private-sender", AuthToken: "private-token",
		Payload: &pb.BusPacket_Alert{Alert: &pb.SystemAlert{SourceComponent: "audit-export", Message: "private-body"}}}
	data, err := proto.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	packet.GetAlert().SourceComponent = "another-source"
	ignored, err := proto.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	h, summary, progress := runGovernanceAuthPackets(t, [][]byte{data, ignored, {}})
	assertGovernanceRefusals(t, summary, progress, 1)
	if summary.Ignored != 2 || summary.Errors != 0 {
		t.Fatalf("ignored = %d, errors = %d; want 2, 0", summary.Ignored, summary.Errors)
	}
	keys, err := h.runtime.rawClient.Keys(context.Background(), "gov:dec:*").Result()
	if err != nil || len(keys) != 0 || strings.Contains(progress, "private-") {
		t.Fatalf("decision keys = %d, err = %v; want 0 and no packet bytes in progress", len(keys), err)
	}
}

func assertGovernanceRefusals(t *testing.T, summary governanceTailSummary, progress string, want int) {
	t.Helper()
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var counters struct {
		Refused int `json:"refused"`
	}
	if err := json.Unmarshal(data, &counters); err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(progress, "tail refused audit packet: service_token\n")
	if counters.Refused != want || lines != want {
		t.Errorf("refused = %d, fixed progress lines = %d; want %d", counters.Refused, lines, want)
	}
}

type governanceSubscriberFunc func(func([]byte) error) error

func (f governanceSubscriberFunc) Subscribe(handler func([]byte) error) error { return f(handler) }

func clearGovernanceTrust(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "CORDUM_POLICY_PUBLIC_KEY_") || strings.HasPrefix(name, "CORDUM_AUDIT_PUBLIC_KEY_") {
			t.Setenv(name, "")
		}
	}
	for _, name := range []string{"SAFETY_POLICY_PUBLIC_KEY", "CORDUM_POLICY_DEV_SIGNING_SEED", "CORDUM_AUDIT_DEV_SIGNING_SEED"} {
		t.Setenv(name, "")
	}
}

func governanceTrustKey(t *testing.T, variable string) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if variable != "" {
		t.Setenv(variable, base64.StdEncoding.EncodeToString(pub))
	}
	return priv
}

func governanceServiceToken(t *testing.T, key ed25519.PrivateKey, subject string) string {
	t.Helper()
	token, err := servicetoken.MintService(key, "cp", subject, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func governanceAuthenticatedPacket(t *testing.T, tenant, token string) []byte {
	t.Helper()
	data := newGovernancePacket(t, governanceAuditEvent{
		Timestamp: time.Now().UTC().Truncate(time.Millisecond), EventType: "safety.decision",
		TenantID: tenant, JobID: "job-" + tenant, Decision: "deny",
	})
	return governancePacketToken(t, data, token)
}

func governancePacketToken(t *testing.T, data []byte, token string) []byte {
	t.Helper()
	var packet pb.BusPacket
	if err := proto.Unmarshal(data, &packet); err != nil {
		t.Fatal(err)
	}
	packet.AuthToken = token
	encoded, err := proto.Marshal(&packet)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func runGovernanceAuthPackets(t *testing.T, packets [][]byte) (*governanceTestHarness, governanceTailSummary, string) {
	t.Helper()
	h := newGovernanceTestHarness(t)
	t.Cleanup(func() {
		for _, close := range []func() error{h.runtime.Close, h.jobStore.Close, h.decisionLogStore.Close} {
			if err := close(); err != nil {
				t.Error(err)
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var progress bytes.Buffer
	summary, err := runGovernanceTail(ctx, fakeGovernanceSubscriber{packets: packets, done: cancel}, h.runtime, &progress)
	if err != context.Canceled {
		t.Fatalf("tail err = %v, want context.Canceled", err)
	}
	return h, summary, progress.String()
}
