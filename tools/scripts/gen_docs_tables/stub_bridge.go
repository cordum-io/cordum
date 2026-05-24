package main

import (
	"context"

	"github.com/cordum/cordum/core/mcp"
)

// stubBridge is a no-op implementation of mcp.ServiceBridge. RegisterAllTools
// requires a non-nil bridge to bind handlers, but this generator only reads
// the static tool metadata (name, description, approval gate) back out of the
// registry, so the handlers are never invoked.
type stubBridge struct{}

var _ mcp.ServiceBridge = stubBridge{}

// Core action surface.
func (stubBridge) SubmitJob(context.Context, mcp.SubmitJobInput) (*mcp.SubmitJobOutput, error) {
	return nil, nil
}
func (stubBridge) CancelJob(context.Context, string, string) error { return nil }
func (stubBridge) TriggerWorkflow(context.Context, mcp.TriggerWorkflowInput) (*mcp.TriggerOutput, error) {
	return nil, nil
}
func (stubBridge) ApproveJob(context.Context, string, string) error { return nil }
func (stubBridge) RejectJob(context.Context, string, string) error  { return nil }
func (stubBridge) SimulatePolicy(context.Context, mcp.PolicySimInput) (*mcp.PolicySimOutput, error) {
	return nil, nil
}

// Read-only discovery surface.
func (stubBridge) ListJobs(context.Context, mcp.ListInput) (*mcp.ListPage, error) { return nil, nil }
func (stubBridge) GetJob(context.Context, string) (*mcp.ResourceItem, error)      { return nil, nil }
func (stubBridge) ListRuns(context.Context, mcp.ListInput) (*mcp.ListPage, error) { return nil, nil }
func (stubBridge) GetRun(context.Context, string) (*mcp.ResourceItem, error)      { return nil, nil }
func (stubBridge) GetRunTimeline(context.Context, string) (*mcp.ResourceItem, error) {
	return nil, nil
}
func (stubBridge) ListWorkflows(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return nil, nil
}
func (stubBridge) ListPacks(context.Context, mcp.ListInput) (*mcp.ListPage, error)   { return nil, nil }
func (stubBridge) ListTopics(context.Context, mcp.ListInput) (*mcp.ListPage, error)  { return nil, nil }
func (stubBridge) ListWorkers(context.Context, mcp.ListInput) (*mcp.ListPage, error) { return nil, nil }
func (stubBridge) ListAgents(context.Context, mcp.ListInput) (*mcp.ListPage, error)  { return nil, nil }
func (stubBridge) ListPendingApprovals(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return nil, nil
}
func (stubBridge) QueryAudit(context.Context, mcp.AuditQueryInput) (*mcp.ListPage, error) {
	return nil, nil
}
func (stubBridge) VerifyAudit(context.Context, string) (*mcp.ResourceItem, error) { return nil, nil }
func (stubBridge) GetStatus(context.Context) (*mcp.ResourceItem, error)           { return nil, nil }

// Mutating administrative surface.
func (stubBridge) CreateWorkflow(context.Context, mcp.CreateWorkflowInput) (*mcp.CreateWorkflowOutput, error) {
	return nil, nil
}
func (stubBridge) InstallPack(context.Context, mcp.InstallPackInput) (*mcp.InstallPackOutput, error) {
	return nil, nil
}
func (stubBridge) UninstallPack(context.Context, mcp.UninstallPackInput) error { return nil }
func (stubBridge) RegisterAgent(context.Context, mcp.RegisterAgentInput) (*mcp.RegisterAgentOutput, error) {
	return nil, nil
}
func (stubBridge) UpdatePolicyBundle(context.Context, mcp.UpdatePolicyBundleInput) (*mcp.UpdatePolicyBundleOutput, error) {
	return nil, nil
}
func (stubBridge) RevokeWorkerSession(context.Context, mcp.RevokeWorkerSessionInput) error {
	return nil
}
func (stubBridge) SetAgentScope(context.Context, mcp.SetAgentScopeInput) (*mcp.SetAgentScopeOutput, error) {
	return nil, nil
}
