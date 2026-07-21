package bus

import (
	"crypto/tls"
	"testing"
)

func TestProductionTransportReadyRequiresVerifiedTLSAndAuthentication(t *testing.T) {
	clientCert := tls.Certificate{Certificate: [][]byte{{1}}}
	tests := []struct {
		name  string
		url   string
		cfg   *tls.Config
		auth  bool
		ready bool
	}{
		{name: "plaintext with auth", url: "nats://broker:4222", auth: true},
		{name: "mixed tls and plaintext servers", url: "tls://a:4222,nats://b:4222", auth: true},
		{name: "tls without auth", url: "tls://broker:4222"},
		{name: "tls with insecure verification", url: "tls://broker:4222", cfg: &tls.Config{InsecureSkipVerify: true}, auth: true}, // #nosec G402 -- negative test.
		{name: "verified tls with token", url: "tls://broker:4222", auth: true, ready: true},
		{name: "verified mutual tls", url: "tls://broker:4222", cfg: &tls.Config{Certificates: []tls.Certificate{clientCert}}, ready: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := productionTransportReady(test.url, test.cfg, test.auth); got != test.ready {
				t.Fatalf("productionTransportReady() = %t, want %t", got, test.ready)
			}
		})
	}
}

func TestAllNATSURLsUseTLSRejectsEmptyAndMixedLists(t *testing.T) {
	for _, raw := range []string{"", " ", "nats://a:4222", "tls://a:4222,nats://b:4222"} {
		if allNATSURLsUseTLS(raw) {
			t.Fatalf("allNATSURLsUseTLS(%q) = true", raw)
		}
	}
	if !allNATSURLsUseTLS(" tls://a:4222 , TLS://b:4222 ") {
		t.Fatal("all verified-TLS server list was rejected")
	}
}

func TestNatsBusProductionTransportReadyFailsClosedOnNil(t *testing.T) {
	var target *NatsBus
	if target.ProductionTransportReady() {
		t.Fatal("nil NatsBus reported authenticated production transport")
	}
}
