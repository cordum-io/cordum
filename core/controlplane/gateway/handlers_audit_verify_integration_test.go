//go:build integration
// +build integration

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type loopbackAuditBus struct {
	mu       sync.Mutex
	handlers map[string][]func(*pb.BusPacket) error
}

func newLoopbackAuditBus() *loopbackAuditBus {
	return &loopbackAuditBus{handlers: make(map[string][]func(*pb.BusPacket) error)}
}

func (b *loopbackAuditBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	handlers := append([]func(*pb.BusPacket) error{}, b.handlers[subject]...)
	b.mu.Unlock()
	for _, h := range handlers {
		if err := h(packet); err != nil {
			return err
		}
	}
	return nil
}

func (b *loopbackAuditBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[subject] = append(b.handlers[subject], handler)
	return nil
}

func TestAuditVerifyEndpointReportsHealthyChainWithNullBackend(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "null")
	apiKey := "test-api-key"

	bufExporter, err := audit.NewExporterFromEnvWithEntitlements(nil)
	if err != nil {
		t.Fatalf("NewExporterFromEnvWithEntitlements: %v", err)
	}
	if bufExporter == nil {
		t.Fatal("expected non-nil buffered exporter for null backend")
	}
	t.Cleanup(func() { _ = bufExporter.Close() })

	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{
			"key":"test-api-key",
			"tenant":"default",
			"role":"admin"
		}]`,
	})
	chainer := audit.NewChainer(s.redisClient(), "")
	s.auditChainer = chainer

	bus := newLoopbackAuditBus()
	consumer, err := audit.NewNATSAuditConsumer(bus, bufExporter.Backend(), audit.WithChainer(chainer))
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}
	t.Cleanup(func() { _ = consumer.Close() })
	publisher := audit.NewNATSAuditPublisher(bus, bufExporter)

	handler, err := newHTTPHandler(s)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	publisher.Send(audit.SIEMEvent{
		EventType: audit.EventSafetyDecision,
		Severity:  audit.SeverityInfo,
		TenantID:  "default",
		Action:    "integration-smoke",
		JobID:     "job-audit-null-backend",
	})

	deadline := time.Now().Add(2 * time.Second)
	var (
		res audit.VerifyResult
		ok  bool
	)
	for time.Now().Before(deadline) {
		resp := doAuditVerifyRequest(t, srv.URL, apiKey)
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
				t.Fatalf("decode verify response: %v", err)
			}
		}()
		if res.Status == audit.VerifyStatusOK && res.TotalEvents >= 1 && len(res.Gaps) == 0 {
			ok = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if !ok {
		t.Fatalf("verify result = %+v, want status=ok total_events>=1 gaps=0", res)
	}
}

func doAuditVerifyRequest(t *testing.T, baseURL, apiKey string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/audit/verify?tenant=default", nil)
	if err != nil {
		t.Fatalf("new audit verify request: %v", err)
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Tenant-ID", "default")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/audit/verify: %v", err)
	}
	return resp
}
