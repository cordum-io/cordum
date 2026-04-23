package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
)

func TestSeedDefaultPackCatalogs(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	if err := packs.SeedDefaultPackCatalogs(ctx, s.configSvc); err != nil {
		t.Fatalf("seed default pack catalogs: %v", err)
	}
	doc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, packs.PackCatalogID)
	if err != nil {
		t.Fatalf("get catalog doc: %v", err)
	}
	if doc == nil || doc.Data == nil {
		t.Fatalf("expected catalog data")
	}
}
