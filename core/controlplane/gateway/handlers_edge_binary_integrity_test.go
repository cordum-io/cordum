package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
)

func validBinaryVerifyOK() model.BinaryVerifyEvent {
	return model.BinaryVerifyEvent{
		Event:       model.BinaryVerifyEventOK,
		Hash:        "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
		Path:        "cordum-gateway",
		SigScheme:   model.BinaryVerifySigSchemeGPG,
		Fingerprint: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		Reason:      "",
		ExitCode:    0,
	}
}

func validBinaryVerifyFail() model.BinaryVerifyEvent {
	return model.BinaryVerifyEvent{
		Event:       model.BinaryVerifyEventFail,
		Hash:        "b1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f91",
		Path:        "cordum-scheduler",
		SigScheme:   model.BinaryVerifySigSchemeGPG,
		Fingerprint: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		Reason:      "hash mismatch cordum-scheduler",
		ExitCode:    1,
	}
}

func newBinaryIntegrityGateway(t *testing.T) *server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auditChainer = audit.NewChainer(s.redisClient(), "")
	return s
}

func postBinaryIntegrityEvents(t *testing.T, s *server, tenant string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := adminCtx(httptest.NewRequest(http.MethodPost,
		"/api/v1/edge/binary-integrity/events", bytes.NewReader(raw)))
	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleIngestBinaryVerify(rec, req)
	return rec
}

func postBinaryIntegrityRaw(t *testing.T, s *server, tenant, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := adminCtx(httptest.NewRequest(http.MethodPost,
		"/api/v1/edge/binary-integrity/events", strings.NewReader(body)))
	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleIngestBinaryVerify(rec, req)
	return rec
}

type binaryVerifyMaxBytesSentinel struct{}

func (binaryVerifyMaxBytesSentinel) Error() string { return "oversized body" }

func (binaryVerifyMaxBytesSentinel) As(target any) bool {
	maxErr, ok := target.(**http.MaxBytesError)
	if !ok {
		return false
	}
	*maxErr = &http.MaxBytesError{Limit: MaxBinaryVerifyRequestBodyBytes}
	return true
}

func TestBinaryIntegrity_MaxBytesErrorTypedCheck(t *testing.T) {
	if !isBinaryVerifyRequestTooLarge(binaryVerifyMaxBytesSentinel{}) {
		t.Fatal("MaxBytesError-compatible value was not detected via errors.As")
	}
	if isBinaryVerifyRequestTooLarge(errors.New("request body too large")) {
		t.Fatal("plain string fallback matched; body-too-large detection must be typed")
	}
}

func listBinaryIntegrityEvents(t *testing.T, s *server, tenant, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/edge/binary-integrity/events?tenant="+tenant+query, nil))
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	s.handleListBinaryVerify(rec, req)
	return rec
}

func TestHandleBinaryIntegrityIngest_TenantDeniedReturnsEdgeError(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	s.auth = &tenantStrictAuth{tenant: "tenant-a", role: "admin"}
	authCtx := &auth.AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "admin-a"}
	raw, err := json.Marshal(binaryVerifyIngestRequest{Events: []model.BinaryVerifyEvent{validBinaryVerifyOK()}})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/binary-integrity/events", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	req.Header.Set("X-Tenant-ID", "tenant-b")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	s.handleIngestBinaryVerify(rec, req)

	assertEdgeErrorShape(t, rec, http.StatusForbidden, edgeErrCodeTenantAccessDenied)
}

func TestHandleBinaryIntegrityIngest_BodyTooLargeReturnsEdgeError(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	body := strings.Repeat("x", int(MaxBinaryVerifyRequestBodyBytes)+1)

	rec := postBinaryIntegrityRaw(t, s, "tenant-a", body)

	assertEdgeErrorShape(t, rec, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge)
}

func TestHandleBinaryIntegrityIngest_InvalidJSONReturnsEdgeError(t *testing.T) {
	s := newBinaryIntegrityGateway(t)

	rec := postBinaryIntegrityRaw(t, s, "tenant-a", `{"events":[`)

	assertEdgeErrorShape(t, rec, http.StatusBadRequest, edgeErrCodeInvalidJSON)
}

func TestHandleBinaryIntegrityIngest_NoEventsReturnsEdgeError(t *testing.T) {
	s := newBinaryIntegrityGateway(t)

	rec := postBinaryIntegrityEvents(t, s, "tenant-a", binaryVerifyIngestRequest{})

	assertEdgeErrorShape(t, rec, http.StatusBadRequest, edgeErrCodeInvalidRequest)
}

func TestBinaryIntegrity_400ReturnsEdgeError(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	badEvent := validBinaryVerifyOK()
	badEvent.Hash = "not-a-sha256"

	rec := postBinaryIntegrityEvents(t, s, "tenant-a", binaryVerifyIngestRequest{
		Events: []model.BinaryVerifyEvent{badEvent},
	})

	assertEdgeErrorShape(t, rec, http.StatusBadRequest, edgeErrCodeInvalidRequest)
	var env edgeErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode edge error envelope: %v", err)
	}
	if _, ok := env.Details["errors"]; !ok {
		t.Fatalf("edge error details missing per-event errors: %#v; body=%s", env.Details, rec.Body.String())
	}
}

func TestHandleBinaryIntegrityList_InvalidQueryReturnsEdgeError(t *testing.T) {
	s := newBinaryIntegrityGateway(t)

	rec := listBinaryIntegrityEvents(t, s, "tenant-a", "&event=maybe")

	assertEdgeErrorShape(t, rec, http.StatusBadRequest, edgeErrCodeInvalidRequest)
}

func TestHandleBinaryIntegrityList_StoreErrorReturnsEdgeError(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auditChainer = nil

	rec := listBinaryIntegrityEvents(t, s, "tenant-a", "")

	assertEdgeErrorShape(t, rec, http.StatusServiceUnavailable, edgeErrCodeServiceUnavailable)
}

func TestHandleIngestBinaryVerify_HappyPathPersists(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	tenant := "tenant-a"

	body := binaryVerifyIngestRequest{
		Endpoint: "host-1",
		Events:   []model.BinaryVerifyEvent{validBinaryVerifyOK(), validBinaryVerifyFail()},
	}
	rec := postBinaryIntegrityEvents(t, s, tenant, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp binaryVerifyIngestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Accepted != 2 || resp.Rejected != 0 {
		t.Fatalf("accepted=%d rejected=%d; want 2/0", resp.Accepted, resp.Rejected)
	}

	listReq := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/edge/binary-integrity/events?tenant="+tenant, nil))
	listReq.Header.Set("X-Tenant-ID", tenant)
	listRec := httptest.NewRecorder()
	s.handleListBinaryVerify(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp binaryVerifyListResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Returned != 2 || len(listResp.Items) != 2 {
		t.Fatalf("returned=%d items=%d; want 2/2", listResp.Returned, len(listResp.Items))
	}
	// Items come back in reverse chronological order — the most recent
	// (the fail event, last appended) leads.
	if listResp.Items[0].Event != model.BinaryVerifyEventFail {
		t.Errorf("items[0].Event = %q; want %q", listResp.Items[0].Event, model.BinaryVerifyEventFail)
	}
	if listResp.Items[0].Endpoint != "host-1" {
		t.Errorf("items[0].Endpoint = %q; want %q", listResp.Items[0].Endpoint, "host-1")
	}
	if listResp.Items[0].Reason != "hash mismatch cordum-scheduler" {
		t.Errorf("items[0].Reason = %q; want hash-mismatch text", listResp.Items[0].Reason)
	}
}

func TestHandleIngestBinaryVerify_RejectsOversizedBatch(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	batch := make([]model.BinaryVerifyEvent, MaxBinaryVerifyEventsPerRequest+1)
	base := validBinaryVerifyOK()
	for i := range batch {
		batch[i] = base
	}
	body := binaryVerifyIngestRequest{Events: batch}
	rec := postBinaryIntegrityEvents(t, s, "tenant-a", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (oversized batch)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "exceeds cap") {
		t.Errorf("body = %q; want 'exceeds cap'", rec.Body.String())
	}
}

func TestHandleIngestBinaryVerify_PerEventValidation(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	bad := validBinaryVerifyOK()
	bad.Hash = "not-a-hash"
	body := binaryVerifyIngestRequest{
		Events: []model.BinaryVerifyEvent{validBinaryVerifyOK(), bad, validBinaryVerifyFail()},
	}
	rec := postBinaryIntegrityEvents(t, s, "tenant-a", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (partial-success); body=%s", rec.Code, rec.Body.String())
	}
	var resp binaryVerifyIngestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Accepted != 2 || resp.Rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d; want 2/1", resp.Accepted, resp.Rejected)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Index != 1 {
		t.Fatalf("errors = %+v; want one entry at index 1", resp.Errors)
	}
	if !strings.Contains(resp.Errors[0].Error, "hash must match") {
		t.Errorf("error message = %q; want 'hash must match' substring", resp.Errors[0].Error)
	}
}

func TestHandleIngestBinaryVerify_EmptyBatch(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	body := binaryVerifyIngestRequest{Events: nil}
	rec := postBinaryIntegrityEvents(t, s, "tenant-a", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (empty batch)", rec.Code)
	}
}

func TestHandleIngestBinaryVerify_AllInvalidReturns400(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	bad := validBinaryVerifyOK()
	bad.Path = "/etc/passwd" // absolute path is rejected
	body := binaryVerifyIngestRequest{Events: []model.BinaryVerifyEvent{bad}}
	rec := postBinaryIntegrityEvents(t, s, "tenant-a", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (zero accepted); body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleListBinaryVerify_EventFilter(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	tenant := "tenant-a"
	events := []model.BinaryVerifyEvent{
		validBinaryVerifyOK(),
		validBinaryVerifyFail(),
		validBinaryVerifyOK(),
	}
	rec := postBinaryIntegrityEvents(t, s, tenant, binaryVerifyIngestRequest{Events: events})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("seed status = %d", rec.Code)
	}

	cases := []struct {
		filter string
		want   int
		kind   string
	}{
		{"ok", 2, model.BinaryVerifyEventOK},
		{"fail", 1, model.BinaryVerifyEventFail},
		{"", 3, ""},
	}
	for _, tc := range cases {
		t.Run("filter="+tc.filter, func(t *testing.T) {
			url := "/api/v1/edge/binary-integrity/events?tenant=" + tenant
			if tc.filter != "" {
				url += "&event=" + tc.filter
			}
			req := adminCtx(httptest.NewRequest(http.MethodGet, url, nil))
			req.Header.Set("X-Tenant-ID", tenant)
			listRec := httptest.NewRecorder()
			s.handleListBinaryVerify(listRec, req)
			if listRec.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", listRec.Code, listRec.Body.String())
			}
			var resp binaryVerifyListResponse
			if err := json.Unmarshal(listRec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Returned != tc.want {
				t.Fatalf("returned = %d; want %d (filter=%q)", resp.Returned, tc.want, tc.filter)
			}
			if tc.kind != "" {
				for i, item := range resp.Items {
					if item.Event != tc.kind {
						t.Errorf("items[%d].Event = %q; want %q", i, item.Event, tc.kind)
					}
				}
			}
		})
	}
}

func TestHandleListBinaryVerify_SigSchemeFilter(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	tenant := "tenant-a"
	gpg := validBinaryVerifyOK()
	dev := validBinaryVerifyOK()
	dev.SigScheme = model.BinaryVerifySigSchemeDev
	dev.Fingerprint = ""
	dev.Hash = "c1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f92"
	body := binaryVerifyIngestRequest{Events: []model.BinaryVerifyEvent{gpg, dev}}
	if rec := postBinaryIntegrityEvents(t, s, tenant, body); rec.Code != http.StatusAccepted {
		t.Fatalf("seed status = %d body=%s", rec.Code, rec.Body.String())
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/edge/binary-integrity/events?sig_scheme=dev&tenant="+tenant, nil))
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	s.handleListBinaryVerify(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp binaryVerifyListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Returned != 1 || resp.Items[0].SigScheme != model.BinaryVerifySigSchemeDev {
		t.Fatalf("returned=%d sig_scheme=%q; want 1 / dev",
			resp.Returned, resp.Items[0].SigScheme)
	}
}

func TestHandleListBinaryVerify_EndpointFilter(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	tenant := "tenant-a"
	if rec := postBinaryIntegrityEvents(t, s, tenant, binaryVerifyIngestRequest{
		Endpoint: "alpha",
		Events:   []model.BinaryVerifyEvent{validBinaryVerifyOK()},
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("seed alpha: %d", rec.Code)
	}
	// Different binary so Append doesn't collide.
	beta := validBinaryVerifyOK()
	beta.Hash = "d1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f93"
	if rec := postBinaryIntegrityEvents(t, s, tenant, binaryVerifyIngestRequest{
		Endpoint: "beta",
		Events:   []model.BinaryVerifyEvent{beta},
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("seed beta: %d", rec.Code)
	}

	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/edge/binary-integrity/events?endpoint=alpha&tenant="+tenant, nil))
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	s.handleListBinaryVerify(rec, req)
	var resp binaryVerifyListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Returned != 1 || resp.Items[0].Endpoint != "alpha" {
		t.Fatalf("returned=%d endpoint=%q; want 1 / alpha",
			resp.Returned, resp.Items[0].Endpoint)
	}
}

func TestHandleListBinaryVerify_TenantIsolation(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	// Seed under tenant-a
	if rec := postBinaryIntegrityEvents(t, s, "tenant-a", binaryVerifyIngestRequest{
		Events: []model.BinaryVerifyEvent{validBinaryVerifyOK()},
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("seed tenant-a: %d", rec.Code)
	}
	// Read as tenant-a admin filtered to tenant-a → see the event.
	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/edge/binary-integrity/events?tenant=tenant-a", nil))
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	s.handleListBinaryVerify(rec, req)
	var resp binaryVerifyListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Returned != 1 {
		t.Fatalf("returned = %d; want 1 (tenant-a)", resp.Returned)
	}
	// A request for tenant-b must not surface tenant-a events.
	reqB := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/edge/binary-integrity/events?tenant=tenant-b", nil))
	reqB.Header.Set("X-Tenant-ID", "tenant-b")
	recB := httptest.NewRecorder()
	s.handleListBinaryVerify(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Fatalf("tenant-b status = %d", recB.Code)
	}
	var respB binaryVerifyListResponse
	if err := json.Unmarshal(recB.Body.Bytes(), &respB); err != nil {
		t.Fatalf("decode B: %v", err)
	}
	if respB.Returned != 0 {
		t.Fatalf("tenant-b returned = %d; want 0 (cross-tenant isolation)", respB.Returned)
	}
}

func TestHandleListBinaryVerify_LimitCap(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	url := fmt.Sprintf("/api/v1/edge/binary-integrity/events?limit=%d&tenant=tenant-a", MaxBinaryVerifyListLimit+50)
	req := adminCtx(httptest.NewRequest(http.MethodGet, url, nil))
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	s.handleListBinaryVerify(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (limit silently capped, not rejected)", rec.Code)
	}
}

func TestHandleListBinaryVerify_InvalidEventReturns400(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/edge/binary-integrity/events?event=maybe&tenant=tenant-a", nil))
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	s.handleListBinaryVerify(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}

func TestHandleListBinaryVerify_AuditChainerMissing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auditChainer = nil
	req := adminCtx(httptest.NewRequest(http.MethodGet,
		"/api/v1/edge/binary-integrity/events?tenant=tenant-a", nil))
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	s.handleListBinaryVerify(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
}

func TestHandleIngestBinaryVerify_RequiresAuditExportPerm(t *testing.T) {
	s := newBinaryIntegrityGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "viewer"}
	authCtx := &auth.AuthContext{Tenant: "default", Role: "viewer", PrincipalID: "v1"}

	body := binaryVerifyIngestRequest{Events: []model.BinaryVerifyEvent{validBinaryVerifyOK()}}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/edge/binary-integrity/events", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleIngestBinaryVerify(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", rec.Code)
	}
}

func TestBinaryVerifyExtraRoundTripAcrossSIEM(t *testing.T) {
	t.Parallel()
	in := validBinaryVerifyFail()
	ts := time.Now().UTC()
	siem := binaryVerifyToSIEMEvent(in, "tenant-a", "host-z", ts)
	if siem.EventType != in.Event {
		t.Errorf("EventType = %q; want %q", siem.EventType, in.Event)
	}
	if siem.Severity != "warning" {
		t.Errorf("Severity = %q; want warning for fail event", siem.Severity)
	}
	out, err := binaryVerifyFromSIEMEvent(siem)
	if err != nil {
		t.Fatalf("recover from SIEM: %v", err)
	}
	if out.BinaryVerifyEvent != in {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", out.BinaryVerifyEvent, in)
	}
	if out.Endpoint != "host-z" {
		t.Errorf("Endpoint = %q; want host-z", out.Endpoint)
	}
}
