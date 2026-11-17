package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/llmingest"
)

func TestLLMIngestCompleteRedaction(t *testing.T) {
	for _, count := range []int{1, 100} {
		t.Run(fmt.Sprintf("secrets_%d", count), func(t *testing.T) {
			secrets := make([]string, count)
			lines := []string{"begin fixture"}
			for i := range secrets {
				secrets[i] = fmt.Sprintf("synthetic-only-fixture-%03d", i)
				lines = append(lines, fmt.Sprintf("password=%q", secrets[i]))
			}
			lines = append(lines, "end fixture")
			stored, response := llmCompleteIngest(t, strings.Join(lines, "\n"))
			remainingStored, remainingResponse := 0, 0
			for _, secret := range secrets {
				if strings.Contains(stored, secret) {
					remainingStored++
				}
				if strings.Contains(response, secret) {
					remainingResponse++
				}
			}
			if remainingStored != 0 {
				t.Errorf("stored content retains %d of %d seeded secrets, want 0", remainingStored, count)
			}
			if remainingResponse != 0 {
				t.Errorf("response content retains %d of %d seeded secrets, want 0", remainingResponse, count)
			}
			for _, text := range []string{stored, response} {
				if !strings.Contains(text, "begin fixture") || !strings.Contains(text, "end fixture") {
					t.Error("redaction must preserve surrounding fixture text")
				}
			}
		})
	}
}

func llmCompleteIngest(t *testing.T, content string) (string, string) {
	t.Helper()
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	session, execution := createLLMIngestParents(t, s, "complete", edgecore.AdapterLLMProxy)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"),
		llmIngestBody("llm-proxy-1", session.SessionID, execution.ExecutionID, "evt-complete", content))
	if rr.Code != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201", rr.Code)
	}
	resp := decodeLLMIngestResponse(t, rr)
	if resp.AcceptedCount != 1 || len(resp.Decisions) != 1 || resp.Decisions[0].Decision != llmingest.DecisionRedact {
		t.Fatal("ingest must accept one event with a redact decision")
	}
	page, err := s.edgeStore.ListEvents(t.Context(), edgecore.ListEventsQuery{
		TenantID: edgeRouteTenant, SessionID: session.SessionID, ExecutionID: execution.ExecutionID,
	})
	if err != nil {
		t.Fatalf("read stored event: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("stored event count = %d, want 1", len(page.Items))
	}
	stored, ok := page.Items[0].InputRedacted["content"].(string)
	if !ok {
		t.Fatal("stored event must contain redacted content")
	}
	return stored, resp.Decisions[0].RedactedContent
}
