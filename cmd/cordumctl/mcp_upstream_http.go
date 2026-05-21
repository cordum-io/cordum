package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	sdk "github.com/cordum/cordum/sdk/client"
)

type mcpUpstreamHTTPClient struct{ client *sdk.Client }

func mcpUpstreamClientOrDefault(api mcpUpstreamAPI, fs *flagSet) mcpUpstreamAPI {
	if api != nil {
		return api
	}
	return mcpUpstreamHTTPClient{client: newClientFromFlags(fs)}
}

func (c mcpUpstreamHTTPClient) ValidateMCPUpstream(ctx context.Context, req mcpUpstreamRequest) (mcpUpstreamValidationResult, error) {
	resp, err := c.client.RequestDetailed(ctx, http.MethodPost, "/api/v1/edge/mcp/upstreams?validate_only=true", req, nil)
	if err != nil {
		return mcpUpstreamValidationResult{}, err
	}
	var out mcpUpstreamValidationResult
	return out, json.Unmarshal(resp.Body, &out)
}

func (c mcpUpstreamHTTPClient) CreateMCPUpstream(ctx context.Context, req mcpUpstreamRequest) (mcpUpstreamRecord, error) {
	resp, err := c.client.RequestDetailed(ctx, http.MethodPost, "/api/v1/edge/mcp/upstreams", req, nil)
	return decodeMCPUpstreamRecord(resp, err)
}

func (c mcpUpstreamHTTPClient) ListMCPUpstreams(ctx context.Context) ([]mcpUpstreamRecord, error) {
	resp, err := c.client.RequestDetailed(ctx, http.MethodGet, "/api/v1/edge/mcp/upstreams", nil, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []mcpUpstreamRecord `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c mcpUpstreamHTTPClient) GetMCPUpstream(ctx context.Context, name string) (mcpUpstreamRecord, error) {
	resp, err := c.client.RequestDetailed(ctx, http.MethodGet, "/api/v1/edge/mcp/upstreams/"+url.PathEscape(name), nil, nil)
	return decodeMCPUpstreamRecord(resp, err)
}

func (c mcpUpstreamHTTPClient) DisableMCPUpstream(ctx context.Context, name string) (mcpUpstreamRecord, error) {
	resp, err := c.client.RequestDetailed(ctx, http.MethodPost, "/api/v1/edge/mcp/upstreams/"+url.PathEscape(name)+"/disable", nil, nil)
	return decodeMCPUpstreamRecord(resp, err)
}

func (c mcpUpstreamHTTPClient) EnableMCPUpstream(ctx context.Context, name string) (mcpUpstreamRecord, error) {
	resp, err := c.client.RequestDetailed(ctx, http.MethodPost, "/api/v1/edge/mcp/upstreams/"+url.PathEscape(name)+"/enable", nil, nil)
	return decodeMCPUpstreamRecord(resp, err)
}

func decodeMCPUpstreamRecord(resp *sdk.RequestDetailedResponse, err error) (mcpUpstreamRecord, error) {
	if err != nil {
		return mcpUpstreamRecord{}, err
	}
	var out mcpUpstreamRecord
	return out, json.Unmarshal(resp.Body, &out)
}
