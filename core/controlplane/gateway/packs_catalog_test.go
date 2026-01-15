package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
)

func TestSeedDefaultPackCatalogs(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	if err := seedDefaultPackCatalogs(ctx, s.configSvc); err != nil {
		t.Fatalf("seed default pack catalogs: %v", err)
	}
	doc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, packCatalogID)
	if err != nil {
		t.Fatalf("get catalog doc: %v", err)
	}
	if doc == nil || doc.Data == nil {
		t.Fatalf("expected catalog data")
	}
}
