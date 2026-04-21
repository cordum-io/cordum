package main

// operationRoute maps a fixture's operationId to the (method, path)
// pair the harness dispatches through the public SDK request surface.
// This table is authored by hand rather than generated from the
// OpenAPI spec so the three harnesses stay byte-identical even while
// their ergonomic namespace facades evolve independently. Step 9's
// parity test proves the Python and TypeScript equivalents resolve the
// same operationIds to the same method+path — divergence there is a
// blocker.
//
// Path placeholders like `{id}` are substituted from the fixture's
// pathParams block at dispatch time.
type operationRoute struct {
	method string
	path   string
}

var operationMap = map[string]operationRoute{
	// Agents
	"createAgent": {"POST", "/api/v1/agents"},
	"listAgents":  {"GET", "/api/v1/agents"},
	"getAgent":    {"GET", "/api/v1/agents/{id}"},
	"updateAgent": {"PUT", "/api/v1/agents/{id}"},
	"deleteAgent": {"DELETE", "/api/v1/agents/{id}"},

	// Jobs
	"submitJob": {"POST", "/api/v1/jobs"},
	"listJobs":  {"GET", "/api/v1/jobs"},
	"getJob":    {"GET", "/api/v1/jobs/{id}"},
	"cancelJob": {"POST", "/api/v1/jobs/{id}/cancel"},

	// Workflows
	"createWorkflow":   {"POST", "/api/v1/workflows"},
	"getWorkflow":      {"GET", "/api/v1/workflows/{id}"},
	"listWorkflows":    {"GET", "/api/v1/workflows"},
	"deleteWorkflow":   {"DELETE", "/api/v1/workflows/{id}"},
	"startWorkflowRun": {"POST", "/api/v1/workflows/{id}/runs"},

	// Policies
	"listPolicyBundles": {"GET", "/api/v1/policy/bundles"},
	"getPolicyBundle":   {"GET", "/api/v1/policy/bundles/{id}"},
	"publishPolicy":     {"POST", "/api/v1/policy/publish"},
	"rollbackPolicy":    {"POST", "/api/v1/policy/rollback"},
	"getPolicyAudit":    {"GET", "/api/v1/policy/audit"},

	// Auth
	"login":         {"POST", "/api/v1/auth/login"},
	"getSession":    {"GET", "/api/v1/auth/session"},
	"getAuthConfig": {"GET", "/api/v1/auth/config"},

	// Streaming
	"streamJob": {"GET", "/api/v1/stream"},
}
