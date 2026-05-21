package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCLIAddValidate(t *testing.T) {
	fake := &fakeMCPUpstreamCLIClient{}
	var stdout, stderr bytes.Buffer
	code := runMCPUpstreamCmd([]string{
		"add",
		"--name", "tenant-tools",
		"--transport", "http",
		"--endpoint", "https://mcp.example.com/tools",
		"--auth-secret-ref", "secret://vault/mcp/tenant-tools",
		"--risk", "medium",
		"--label", "team=platform",
		"--validate-only",
	}, &stdout, &stderr, fake)
	if code != 0 {
		t.Fatalf("valid validate-only exit = %d stderr=%s", code, stderr.String())
	}
	if fake.validateCalls != 1 || fake.createCalls != 0 {
		t.Fatalf("calls validate=%d create=%d, want validate=1 create=0", fake.validateCalls, fake.createCalls)
	}
	if fake.lastRequest.Name != "tenant-tools" || fake.lastRequest.AuthSecretRef != "secret://vault/mcp/tenant-tools" {
		t.Fatalf("request = %#v", fake.lastRequest)
	}
	if !strings.Contains(stdout.String(), "valid") {
		t.Fatalf("stdout missing valid verdict: %q", stdout.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "sk-test-raw-token") {
		t.Fatalf("CLI leaked raw token: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	fake.invalid = true
	stdout.Reset()
	stderr.Reset()
	code = runMCPUpstreamCmd([]string{
		"add",
		"--name", "local-tools",
		"--transport", "http",
		"--endpoint", "http://localhost:8080",
		"--auth-secret-ref", "secret://vault/mcp/local-tools",
		"--validate-only",
	}, &stdout, &stderr, fake)
	if code == 0 {
		t.Fatalf("invalid validate-only exit = 0 stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if fake.createCalls != 0 {
		t.Fatalf("invalid validate-only created %d record(s)", fake.createCalls)
	}
	if !strings.Contains(stderr.String(), "invalid") || !strings.Contains(stderr.String(), "unsafe endpoint") {
		t.Fatalf("stderr missing invalid reason: %q", stderr.String())
	}
}

func TestCLIValidateSubcommand(t *testing.T) {
	fake := &fakeMCPUpstreamCLIClient{}
	var stdout, stderr bytes.Buffer
	code := runMCPUpstreamCmd([]string{
		"validate",
		"--name", "tenant-tools",
		"--transport", "http",
		"--endpoint", "https://mcp.example.com/tools",
		"--auth-secret-ref", "secret://vault/mcp/tenant-tools",
	}, &stdout, &stderr, fake)
	if code != 0 {
		t.Fatalf("validate exit = %d stderr=%s", code, stderr.String())
	}
	if fake.validateCalls != 1 || fake.createCalls != 0 {
		t.Fatalf("calls validate=%d create=%d, want validate=1 create=0", fake.validateCalls, fake.createCalls)
	}
	if !strings.Contains(stdout.String(), "valid") {
		t.Fatalf("stdout missing valid verdict: %q", stdout.String())
	}
}

type fakeMCPUpstreamCLIClient struct {
	validateCalls int
	createCalls   int
	invalid       bool
	lastRequest   mcpUpstreamRequest
}

func (f *fakeMCPUpstreamCLIClient) ValidateMCPUpstream(_ context.Context, req mcpUpstreamRequest) (mcpUpstreamValidationResult, error) {
	f.validateCalls++
	f.lastRequest = req
	if f.invalid {
		return mcpUpstreamValidationResult{Valid: false, Reason: "unsafe endpoint"}, nil
	}
	return mcpUpstreamValidationResult{Valid: true}, nil
}

func (f *fakeMCPUpstreamCLIClient) CreateMCPUpstream(_ context.Context, req mcpUpstreamRequest) (mcpUpstreamRecord, error) {
	f.createCalls++
	f.lastRequest = req
	return mcpUpstreamRecord{Name: req.Name, TenantID: req.TenantID, Enabled: true}, nil
}

func (f *fakeMCPUpstreamCLIClient) ListMCPUpstreams(context.Context) ([]mcpUpstreamRecord, error) {
	return nil, errors.New("not used")
}

func (f *fakeMCPUpstreamCLIClient) GetMCPUpstream(context.Context, string) (mcpUpstreamRecord, error) {
	return mcpUpstreamRecord{}, errors.New("not used")
}

func (f *fakeMCPUpstreamCLIClient) DisableMCPUpstream(context.Context, string) (mcpUpstreamRecord, error) {
	return mcpUpstreamRecord{}, errors.New("not used")
}

func (f *fakeMCPUpstreamCLIClient) EnableMCPUpstream(context.Context, string) (mcpUpstreamRecord, error) {
	return mcpUpstreamRecord{}, errors.New("not used")
}
