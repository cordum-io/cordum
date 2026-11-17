package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/stretchr/testify/require"
)

func TestPolicyAuditPublishRequiresDurableRecord(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "record_written"
		if fail {
			name = "record_unavailable"
		}
		t.Run(name, func(t *testing.T) { policyAuditPublishCheck(t, fail) })
	}
}

func policyAuditPublishCheck(t *testing.T, fail bool) {
	t.Helper()
	s, mr, hooked, _ := auditPersistServer(t)
	seed, err := json.Marshal(struct {
		Content string `json:"content"`
		Enabled bool   `json:"enabled"`
	}{Content: policyContent})
	require.NoError(t, err)
	req := policyAuditPublishRequest(http.MethodPut, "/api/v1/policy/bundles/secops/test", seed)
	req.SetPathValue("id", "secops/test")
	rec := httptest.NewRecorder()
	s.handlePutPolicyBundle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "seed: %s", rec.Body.String())
	before := auditPersistRaw(t, mr, auditPersistPolicyKey)
	identity := policyAuditPublishIdentity(t, s)
	if fail {
		hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
		hooked.AddHook(auditPersistHook{failKey: auditPersistWriteKey})
	}
	rec = httptest.NewRecorder()
	s.handlePublishPolicyBundles(rec, policyAuditPublishRequest(http.MethodPost, "/api/v1/policy/publish", []byte(`{}`)))
	after := auditPersistRaw(t, mr, auditPersistPolicyKey)
	afterIdentity := policyAuditPublishIdentity(t, s)
	recorded := slices.Contains(auditPersistActions(t, s), "publish")
	if fail {
		if rec.Code < 400 || before != after || identity != afterIdentity {
			t.Errorf("audit failure must leave policy unchanged: status=%d bundles_unchanged=%t identity_unchanged=%t", rec.Code, before == after, identity == afterIdentity)
		}
		if recorded {
			t.Error("fault did not prevent the publish audit record")
		}
	} else if rec.Code != http.StatusOK || before == after || identity == afterIdentity || !recorded {
		t.Errorf("durable publish: status=%d bundles_changed=%t identity_changed=%t recorded=%t", rec.Code, before != after, identity != afterIdentity, recorded)
	}
}

func policyAuditPublishRequest(method, path string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	req.Header.Set("X-Principal-Id", "user1")
	return req
}

func policyAuditPublishIdentity(t *testing.T, s *server) string {
	t.Helper()
	bundles, _, err := s.loadPolicyBundlesWithDoc(context.Background())
	require.NoError(t, err)
	_, identity, err := policybundles.BuildPolicyFromBundles(bundles)
	require.NoError(t, err)
	return identity
}
