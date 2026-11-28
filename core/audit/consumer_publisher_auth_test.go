package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/policysign"
)

func publisherAuthKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func publisherAuthToken(t *testing.T, priv ed25519.PrivateKey, kid, subject string, at time.Time) string {
	t.Helper()
	token, err := servicetoken.MintService(priv, kid, subject, at)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// publisherAuthSession signs a cordum-session token, the worker session format.
func publisherAuthSession(t *testing.T, priv ed25519.PrivateKey, kid, subject string) string {
	t.Helper()
	now := time.Now().UTC()
	token, err := servicetoken.Sign(priv, kid, servicetoken.Claims{Subject: subject, Tenant: "default",
		SDKVersion: "v2.17.0", JTI: "session-jti", IssuedAt: now, ExpiresAt: now.Add(time.Hour)}, servicetoken.TypSession)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// TestNATSAuditConsumer_PublisherAuth presents each publisher credential to
// the consumer and counts what reached the chain, the SIEM exporter and the
// drop counter.
func TestNATSAuditConsumer_PublisherAuth(t *testing.T) {
	cpPub, cpPriv := publisherAuthKey(t)
	mcpPub, mcpPriv := publisherAuthKey(t)
	kernelPub, kernelPriv := publisherAuthKey(t)
	_, otherPriv := publisherAuthKey(t)
	controlPlane := policysign.NewTrustStore()
	if err := controlPlane.Add("cp", cpPub); err != nil {
		t.Fatal(err)
	}
	publishers := map[string]ed25519.PublicKey{
		servicetoken.IdentityMCP: mcpPub, servicetoken.IdentitySafetyKernel: kernelPub,
	}
	now, expired := time.Now(), time.Now().Add(-time.Hour)
	cases := []struct {
		name     string
		token    string
		accepted bool
	}{
		{"api-gateway", publisherAuthToken(t, cpPriv, "cp", servicetoken.IdentityGateway, now), true},
		{"cordum-scheduler", publisherAuthToken(t, cpPriv, "cp", servicetoken.IdentityScheduler, now), true},
		{"cordum-mcp", publisherAuthToken(t, mcpPriv, "default", servicetoken.IdentityMCP, now), true},
		{"cordum-safety-kernel", publisherAuthToken(t, kernelPriv, "default", servicetoken.IdentitySafetyKernel, now), true},
		{"no token", "", false},
		{"worker session", publisherAuthSession(t, cpPriv, "cp", servicetoken.IdentityGateway), false},
		{"session under a publisher key", publisherAuthSession(t, mcpPriv, "default", servicetoken.IdentityMCP), false},
		{"untrusted key", publisherAuthToken(t, otherPriv, "cp", servicetoken.IdentityGateway, now), false},
		{"workflow-engine", publisherAuthToken(t, cpPriv, "cp", servicetoken.IdentityWorkflow, now), false},
		{"cordum-mcp key minting api-gateway", publisherAuthToken(t, mcpPriv, "default", servicetoken.IdentityGateway, now), false},
		{"expired control-plane token", publisherAuthToken(t, cpPriv, "cp", servicetoken.IdentityGateway, expired), false},
		{"expired publisher token", publisherAuthToken(t, mcpPriv, "default", servicetoken.IdentityMCP, expired), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus, exporter := &mockAuditBus{}, &mockExporter{}
			chainer, client := newConsumerChainer(t)
			if _, err := NewNATSAuditConsumer(bus, exporter, WithChainer(chainer),
				WithPublisherAuth(controlPlane, publishers)); err != nil {
				t.Fatal(err)
			}
			packet := packetFor(t, testEvent())
			packet.AuthToken = tc.token
			drops := testutil.ToFloat64(auditEventDrops.WithLabelValues("service_token"))
			if err := subscribedHandler(t, bus)(packet); err != nil {
				t.Fatalf("handler: %v", err)
			}
			chained, err := client.XLen(context.Background(), chainer.StreamKey(testEvent().TenantID)).Result()
			if err != nil {
				t.Fatal(err)
			}
			dropped := testutil.ToFloat64(auditEventDrops.WithLabelValues("service_token")) - drops
			want := 0
			if tc.accepted {
				want = 1
			}
			if exporter.totalEvents() != want || chained != int64(want) || dropped != float64(1-want) {
				t.Fatalf("exported %d, chained %d, dropped %v; want %d, %d, %d",
					exporter.totalEvents(), chained, dropped, want, want, 1-want)
			}
		})
	}
}

// TestNATSAuditConsumer_PublisherAuthRefusalIsVisible shows that an empty
// trust refuses a well-formed token, and that the one refusal log line holds
// no caller-controlled data.
func TestNATSAuditConsumer_PublisherAuthRefusalIsVisible(t *testing.T) {
	_, priv := publisherAuthKey(t)
	bus, exporter := &mockAuditBus{}, &mockExporter{}
	if _, err := NewNATSAuditConsumer(bus, exporter, WithPublisherAuth(policysign.NewTrustStore(), nil)); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	event := testEvent()
	packet := packetFor(t, event)
	packet.SenderId = "sender-from-packet"
	packet.AuthToken = publisherAuthToken(t, priv, "default", servicetoken.IdentityGateway, time.Now())
	drops := testutil.ToFloat64(auditEventDrops.WithLabelValues("service_token"))
	if err := subscribedHandler(t, bus)(packet); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := testutil.ToFloat64(auditEventDrops.WithLabelValues("service_token")) - drops; got != 1 || exporter.totalEvents() != 0 {
		t.Fatalf("dropped %v and exported %d, want 1 and 0", got, exporter.totalEvents())
	}
	out := logs.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, `msg="audit export event refused" reason=service_token`) {
		t.Fatalf("want one fixed refusal line, got %q", out)
	}
	for _, leaked := range []string{packet.AuthToken, packet.SenderId, servicetoken.IdentityGateway,
		event.TenantID, event.AgentID, event.JobID, event.Action, event.EventType} {
		if strings.Contains(out, leaked) {
			t.Fatalf("refusal log carries %q: %q", leaked, out)
		}
	}
}
