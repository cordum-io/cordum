package v1

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Skipf("fixture %s not found: %v", name, err)
	}
	return data
}

func unmarshalFixture(t *testing.T, name string) *BusPacket {
	t.Helper()
	data := loadFixture(t, name)
	var pkt BusPacket
	if err := proto.Unmarshal(data, &pkt); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return &pkt
}

func loadConformancePublicKey(t *testing.T) *ecdsa.PublicKey {
	t.Helper()
	data := loadFixture(t, "public_key.pem")
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("public key PEM decode failed")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	key, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		t.Fatal("public key is not ECDSA")
	}
	return key
}

func verifyConformanceSignature(t *testing.T, pkt *BusPacket, pub *ecdsa.PublicKey) {
	t.Helper()
	if len(pkt.GetSignature()) == 0 {
		t.Fatal("missing signature")
	}
	clone := proto.Clone(pkt).(*BusPacket)
	clone.Signature = nil
	unsigned, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		t.Fatalf("marshal unsigned: %v", err)
	}
	hash := sha256.Sum256(unsigned)
	if !ecdsa.VerifyASN1(pub, hash[:], pkt.GetSignature()) {
		t.Fatal("signature verification failed")
	}
}

func TestConformanceHandshake(t *testing.T) {
	pub := loadConformancePublicKey(t)
	pkt := unmarshalFixture(t, "buspacket_handshake.bin")
	verifyConformanceSignature(t, pkt, pub)

	if pkt.GetProtocolVersion() != 1 {
		t.Fatalf("protocol_version = %d, want 1", pkt.GetProtocolVersion())
	}

	hs := pkt.GetHandshake()
	if hs == nil {
		t.Fatal("expected handshake payload")
	}
	if hs.GetComponentId() != "worker-1" {
		t.Fatalf("component_id = %q, want %q", hs.GetComponentId(), "worker-1")
	}
	if hs.GetRole() != ComponentRole_COMPONENT_ROLE_WORKER {
		t.Fatalf("role = %v, want WORKER", hs.GetRole())
	}
	if len(hs.GetSupportedVersions()) != 1 || hs.GetSupportedVersions()[0] != 1 {
		t.Fatalf("supported_versions = %v, want [1]", hs.GetSupportedVersions())
	}
	caps := hs.GetCapabilities()
	if !caps["signatures"] || !caps["progress"] || !caps["cancel"] || caps["compensation"] {
		t.Fatalf("capabilities = %v", caps)
	}
	if hs.GetSdkVersion() != "2.0.19" {
		t.Fatalf("sdk_version = %q, want %q", hs.GetSdkVersion(), "2.0.19")
	}
}

func TestConformanceEnhancedAlert(t *testing.T) {
	pub := loadConformancePublicKey(t)
	pkt := unmarshalFixture(t, "buspacket_alert_enhanced.bin")
	verifyConformanceSignature(t, pkt, pub)

	if pkt.GetProtocolVersion() != 1 {
		t.Fatalf("protocol_version = %d, want 1", pkt.GetProtocolVersion())
	}

	alert := pkt.GetAlert()
	if alert == nil {
		t.Fatal("expected alert payload")
	}

	// Structured fields (replacements for deprecated level/component/code).
	if alert.GetSeverity() != AlertSeverity_ALERT_SEVERITY_CRITICAL {
		t.Fatalf("severity = %v, want CRITICAL", alert.GetSeverity())
	}
	if alert.GetErrorCodeEnum() != ErrorCode_ERROR_CODE_PROTOCOL_SIGNATURE_INVALID {
		t.Fatalf("error_code_enum = %v, want PROTOCOL_SIGNATURE_INVALID", alert.GetErrorCodeEnum())
	}
	if alert.GetSourceComponent() != "scheduler-1" {
		t.Fatalf("source_component = %q, want %q", alert.GetSourceComponent(), "scheduler-1")
	}
	details := alert.GetDetails()
	if details["sender"] != "worker-bad" {
		t.Fatalf("details[sender] = %q, want %q", details["sender"], "worker-bad")
	}
	if details["subject"] != "sys.job.result" {
		t.Fatalf("details[subject] = %q, want %q", details["subject"], "sys.job.result")
	}
	if alert.GetTraceId() != "trace-offending-packet" {
		t.Fatalf("trace_id = %q, want %q", alert.GetTraceId(), "trace-offending-packet")
	}
}

func TestConformanceAllFixtures(t *testing.T) {
	pub := loadConformancePublicKey(t)

	fixtures := []struct {
		name    string
		payload string
	}{
		{"buspacket_job_request.bin", "job_request"},
		{"buspacket_job_result.bin", "job_result"},
		{"buspacket_heartbeat.bin", "heartbeat"},
		{"buspacket_job_progress.bin", "job_progress"},
		{"buspacket_job_cancel.bin", "job_cancel"},
		{"buspacket_alert.bin", "alert"},
	}

	for _, tc := range fixtures {
		t.Run(tc.payload, func(t *testing.T) {
			pkt := unmarshalFixture(t, tc.name)
			verifyConformanceSignature(t, pkt, pub)
			if pkt.GetProtocolVersion() != 1 {
				t.Fatalf("protocol_version = %d, want 1", pkt.GetProtocolVersion())
			}
		})
	}
}
