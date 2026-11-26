package gateway

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/redis/go-redis/v9"
)

func TestPackInstallWithoutSystemDefault(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	if _, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "default"); !errors.Is(err, redis.Nil) {
		t.Fatalf("expected missing system/default, got %v", err)
	}
	bundle := buildTarGz(t, map[string]string{
		"pack.yaml": `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: nodefault-pack
  version: 0.1.0
compatibility:
  protocolVersion: 1
topics:
  - name: job.nodefault-pack.run
    inputSchemaId: nodefault-pack/In
resources:
  schemas:
    - id: nodefault-pack/In
      path: schemas/In.json
`,
		"schemas/In.json": `{"type":"object"}`,
	})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("bundle", "nodefault-pack.tgz")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(bundle); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/packs/install", &body)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()
	s.handleInstallPack(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	reg, _, err := s.topicRegistry.Get(ctx, "job.nodefault-pack.run")
	if err != nil {
		t.Fatalf("topic not registered: %v", err)
	}
	if reg == nil || reg.PackID != "nodefault-pack" || reg.Pool != "" || reg.Status != topicregistry.StatusDisabled {
		t.Fatalf("unexpected topic registration: %+v", reg)
	}
}
