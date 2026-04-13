package main

import (
	"context"
	"net/http"
	"net/url"
)

func (c *restClient) listTopics(ctx context.Context) (topicListResponse, error) {
	var resp topicListResponse
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/topics", nil, &resp)
	return resp, err
}

func (c *restClient) createTopic(ctx context.Context, req topicRegistration) (topicRegistration, error) {
	var resp topicRegistration
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/topics", req, &resp)
	return resp, err
}

func (c *restClient) deleteTopic(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/topics/"+url.PathEscape(name), nil, nil)
}

func (c *restClient) listCredentials(ctx context.Context) (workerCredentialListResponse, error) {
	var resp workerCredentialListResponse
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/workers/credentials", nil, &resp)
	return resp, err
}

func (c *restClient) createCredential(ctx context.Context, req workerCredentialCreateRequest) (issuedWorkerCredential, error) {
	var resp issuedWorkerCredential
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/workers/credentials", req, &resp)
	return resp, err
}

func (c *restClient) revokeCredential(ctx context.Context, workerID string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/workers/credentials/"+url.PathEscape(workerID), nil, nil)
}
