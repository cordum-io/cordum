package actiongates

// ProductionPipelineOptions wires production dependencies into the gate
// pipeline factory. Every dependency is optional — missing dependencies
// degrade individual gates to a fail-closed posture without taking down
// the entire pipeline, so a misconfigured deployment still rejects
// destructive actions instead of silently allowing them.
//
// Required pairings to keep gates functional in production:
//
//   - MutationGate needs an Approvals lookup — without it, every
//     destructive action fails closed at the mutation gate with
//     Code=internal_error.
//   - ProvenanceGate needs both an Approvals lookup AND a ChainVerifier.
//     Gateway production wiring supplies a core/audit ChainVerifier; if
//     that dependency is absent or unavailable, destructive actions fail
//     closed with Code=service_unavailable rather than silently allowing
//     or surfacing an ambiguous internal_error.
//   - MCPGate needs an Identities resolver — without it, every
//     mcp_call action fails closed with Code=internal_error.
//
// File / URL / Tenant gates have no required deps (Resolver/DomainSeen
// on URL are optional; default uses net.DefaultResolver and a never-seen
// stance for unknown hosts).
type ProductionPipelineOptions struct {
	Approvals           ApprovalLookup
	Resources           ResourceLookup
	Identities          MCPIdentityResolver
	Reachability        ReachabilityProbe
	ChainVerifier       ChainVerifier
	HostResolver        HostResolver
	DomainSeen          func(host string) bool
	DangerousParamRules map[string][]DangerousParamRule
	// DestructiveToolGlobs is forwarded to the MCPGate's content-aware
	// session-taint deny (tool-name globs treated as destructive). Unset => the
	// gate's defaultDestructiveToolGlobs.
	DestructiveToolGlobs []string
	// DestructiveMutationArgKeys names string args to scan for GraphQL mutation
	// documents. Unset => the gate's built-in defaults.
	DestructiveMutationArgKeys []string
	// DestructiveMutationFieldGlobs lists GraphQL mutation field globs treated as
	// destructive. Unset => the gate's built-in defaults.
	DestructiveMutationFieldGlobs []string
	// FailClosedDestructiveOnTaintLookupError forwards the default-off rollout
	// flag for requiring human approval when a destructive MCP call cannot prove
	// the session is clean because the taint lookup errored.
	FailClosedDestructiveOnTaintLookupError bool
}

// BuildProductionPipeline constructs the canonical Tenant→File→URL→MCP→
// Mutation→Provenance pipeline used by both the gateway HTTP path and
// the safety-kernel gRPC path. Gate ordering is enforced inside NewPipeline.
//
// The factory never returns an error so callers can declare wiring at
// service-start time without an extra failure mode; nil dependencies
// degrade individual gates as documented on ProductionPipelineOptions.
// Callers that want a strict construction-time error should validate
// opts before invocation.
func BuildProductionPipeline(opts ProductionPipelineOptions) *Pipeline {
	return NewPipeline(
		NewTenantGate(),
		NewFileGate(),
		NewURLGate(URLGateOptions{
			Resolver:   opts.HostResolver,
			DomainSeen: opts.DomainSeen,
		}),
		NewMCPGate(MCPGateOptions{
			Identities:                              opts.Identities,
			Reachability:                            opts.Reachability,
			DangerousParamRules:                     opts.DangerousParamRules,
			DestructiveToolGlobs:                    opts.DestructiveToolGlobs,
			DestructiveMutationArgKeys:              opts.DestructiveMutationArgKeys,
			DestructiveMutationFieldGlobs:           opts.DestructiveMutationFieldGlobs,
			FailClosedDestructiveOnTaintLookupError: opts.FailClosedDestructiveOnTaintLookupError,
		}),
		NewMutationGate(MutationGateOptions{
			Approvals: opts.Approvals,
			Resources: opts.Resources,
		}),
		NewProvenanceGate(ProvenanceGateOptions{
			Approvals:     opts.Approvals,
			ChainVerifier: opts.ChainVerifier,
		}),
	)
}
