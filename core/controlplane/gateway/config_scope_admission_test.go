package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policyshadow"
)

type configCaller struct {
	tenant, target string
	cross          bool
}

var (
	configOwner = configCaller{tenant: "tenant-b", target: "tenant-b"}
	configOther = configCaller{tenant: "tenant-a", target: "tenant-a"}
	configCross = configCaller{tenant: "tenant-a", target: "tenant-b", cross: true}
)

func newConfigAdmissionGateway(t *testing.T) (*server, *stubBus, http.Handler) {
	t.Helper()
	s, bus, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatal(err)
	}
	return s, bus, mux
}

func (c configCaller) do(mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := tenantObjectRequest(method, path, c.tenant, "")
	r.Header.Set("X-Tenant-ID", c.target)
	auth.FromRequest(r).AllowCrossTenant = c.cross
	r.Body = io.NopCloser(strings.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	return rr
}

func (c configCaller) write(mux http.Handler, method, scope, id, value string) *httptest.ResponseRecorder {
	return c.do(mux, method, "/api/v1/config", fmt.Sprintf(`{"scope":%q,"scope_id":%q,"data":{"value":%q}}`, scope, id, value))
}

// envelope returns the raw GET envelope=true body and its decoded document.
func (c configCaller) envelope(t *testing.T, mux http.Handler, scope, id string) (string, configsvc.Document) {
	t.Helper()
	rr := c.do(mux, http.MethodGet, "/api/v1/config?envelope=true&scope="+url.QueryEscape(scope)+"&scope_id="+url.QueryEscape(id), "")
	var doc configsvc.Document
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &doc) != nil {
		t.Fatalf("GET %s/%s as %s: %d %s", scope, id, c.tenant, rr.Code, rr.Body.String())
	}
	return rr.Body.String(), doc
}

func publishedCount(bus *stubBus) int {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	return len(bus.published)
}

// configAdmissionSetup seeds the store and returns the caller, the scope and
// scope_id it sends, and a reader for the document the request must not change.
type configAdmissionSetup func(t *testing.T, s *server, mux http.Handler) (configCaller, string, string, func() string)

// colonAlias seeds tenant-b's document at scope_id "shared:tail" and sends a
// scope that joins the scope name to the stored id's prefix.
func colonAlias(scope string, caller configCaller) configAdmissionSetup {
	return func(t *testing.T, _ *server, mux http.Handler) (configCaller, string, string, func() string) {
		if rr := configOwner.write(mux, http.MethodPut, scope, "shared:tail", "private-b"); rr.Code != http.StatusNoContent {
			t.Fatalf("seed %s: %d %s", scope, rr.Code, rr.Body.String())
		}
		_, doc := configOwner.envelope(t, mux, scope, "shared:tail")
		target := func() string { body, _ := configOwner.envelope(t, mux, scope, "shared:tail"); return body }
		return caller, scope + ":" + strings.TrimSuffix(doc.ScopeID, ":tail"), "tail", target
	}
}

// storedDocument returns a reader for the raw stored document at scope/id.
func storedDocument(t *testing.T, s *server, scope configsvc.Scope, id string) func() string {
	return func() string {
		doc, err := s.configSvc.Get(context.Background(), scope, id)
		data, _ := json.Marshal(doc)
		return fmt.Sprint(string(data), err)
	}
}

func undeclaredScope(caller configCaller, scope, id string) configAdmissionSetup {
	return func(t *testing.T, s *server, _ http.Handler) (configCaller, string, string, func() string) {
		return caller, scope, id, storedDocument(t, s, configsvc.Scope(scope), id)
	}
}

func TestTenantIsolationConfigScopeAdmission(t *testing.T) {
	t.Run("declared", func(t *testing.T) {
		for _, tc := range []struct{ scope, id string }{
			{"system", "default"}, {"org", "tenant-b"}, {"team", "admitted"}, {"workflow", "admitted"}, {"step", "admitted"},
		} {
			t.Run(tc.scope, func(t *testing.T) {
				_, _, mux := newConfigAdmissionGateway(t)
				if rr := configOwner.write(mux, http.MethodPut, tc.scope, tc.id, "value-b"); rr.Code != http.StatusNoContent {
					t.Fatalf("PUT: %d %s", rr.Code, rr.Body.String())
				}
				if _, doc := configOwner.envelope(t, mux, tc.scope, tc.id); doc.Data["value"] != "value-b" {
					t.Errorf("stored value = %v, want value-b", doc.Data["value"])
				}
			})
		}
		t.Run("org-cross-tenant", func(t *testing.T) {
			_, _, mux := newConfigAdmissionGateway(t)
			if rr := configCross.write(mux, http.MethodPut, "org", "tenant-b", "selected"); rr.Code != http.StatusNoContent {
				t.Fatalf("PUT: %d %s", rr.Code, rr.Body.String())
			}
			if _, doc := configOwner.envelope(t, mux, "org", "tenant-b"); doc.Data["value"] != "selected" {
				t.Errorf("stored value = %v, want selected", doc.Data["value"])
			}
		})
	})

	t.Run("undeclared", func(t *testing.T) {
		cases := []struct {
			name  string
			setup configAdmissionSetup
		}{
			{"team-alias", colonAlias("team", configOther)},
			{"workflow-alias", colonAlias("workflow", configOther)},
			{"step-alias", colonAlias("step", configOther)},
			{"cross-tenant-alias", colonAlias("team", configCross)},
			{"policy_shadow", func(t *testing.T, s *server, _ http.Handler) (configCaller, string, string, func() string) {
				if err := s.configSvc.Set(context.Background(), &configsvc.Document{
					Scope: policyshadow.ShadowScope, ScopeID: "tenant-b", Data: map[string]any{"value": "private-b"},
				}); err != nil {
					t.Fatal(err)
				}
				return configOther, string(policyshadow.ShadowScope), "tenant-b", storedDocument(t, s, policyshadow.ShadowScope, "tenant-b")
			}},
			{"System", undeclaredScope(configOther, "System", "default")},
			{"gateway", undeclaredScope(configOther, mcpArgumentRedactionConfigScope, mcpArgumentRedactionConfigKey)},
		}
		for _, tc := range cases {
			for _, method := range []string{http.MethodPut, http.MethodPost} {
				t.Run(tc.name+"-"+method, func(t *testing.T) {
					s, bus, mux := newConfigAdmissionGateway(t)
					caller, scope, id, target := tc.setup(t, s, mux)
					before, published := target(), publishedCount(bus)
					tenantObjectUnchanged(t, s)
					rr := caller.write(mux, method, scope, id, "private-a")
					var body struct{ Code, Error string }
					_ = json.Unmarshal(rr.Body.Bytes(), &body)
					if rr.Code != http.StatusBadRequest || body.Code != errorCodeConfigRequestInvalid || body.Error != "invalid scope" {
						t.Errorf("%s scope %q: status=%d body=%s", method, scope, rr.Code, rr.Body.String())
					}
					if after := target(); after != before {
						t.Errorf("target document changed:\nbefore=%s\nafter=%s", before, after)
					}
					if n := publishedCount(bus); n != published {
						t.Errorf("config-changed published %d messages", n-published)
					}
				})
			}
		}
	})
}
