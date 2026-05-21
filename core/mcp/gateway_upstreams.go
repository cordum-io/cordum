package mcp

import (
	"context"

	"github.com/cordum/cordum/core/edge"
)

func (g *Gateway) upstreamCount(ctx context.Context, tenantID string) int {
	upstreams, err := g.listUpstreams(ctx, tenantID)
	if err != nil {
		return 0
	}
	return len(upstreams)
}

func (g *Gateway) selectUsableUpstream(ctx context.Context, tenantID string) (*edge.UpstreamServer, bool, error) {
	upstreams, err := g.listUpstreams(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	for i := range upstreams {
		if !upstreams[i].Enabled {
			continue
		}
		if err := edge.RevalidateMCPUpstreamAtUse(ctx, &upstreams[i]); err != nil {
			return nil, false, err
		}
		return &upstreams[i], true, nil
	}
	return nil, false, nil
}

func (g *Gateway) listUpstreams(ctx context.Context, tenantID string) ([]edge.UpstreamServer, error) {
	if g.deps.UpstreamRegistry == nil {
		return nil, nil
	}
	return g.deps.UpstreamRegistry.List(ctx, tenantID)
}
