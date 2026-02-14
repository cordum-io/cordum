package packs

// PackManifest is the parsed pack.yaml manifest structure.
type PackManifest struct {
	APIVersion    string            `yaml:"apiVersion"`
	Kind          string            `yaml:"kind"`
	Metadata      PackMetadata      `yaml:"metadata"`
	Compatibility PackCompatibility `yaml:"compatibility"`
	Topics        []PackTopic       `yaml:"topics"`
	Resources     PackResources     `yaml:"resources"`
	Overlays      PackOverlays      `yaml:"overlays"`
	Tests         PackTests         `yaml:"tests"`
}

// PackMetadata holds pack identity information.
type PackMetadata struct {
	ID          string `yaml:"id" json:"id"`
	Version     string `yaml:"version" json:"version"`
	Title       string `yaml:"title" json:"title"`
	Description string `yaml:"description" json:"description"`
}

// PackCompatibility declares protocol and core version requirements.
type PackCompatibility struct {
	ProtocolVersion int    `yaml:"protocolVersion" json:"protocolVersion"`
	MinCoreVersion  string `yaml:"minCoreVersion" json:"minCoreVersion"`
	MaxCoreVersion  string `yaml:"maxCoreVersion" json:"maxCoreVersion"`
}

// PackTopic describes a job topic provided by the pack.
type PackTopic struct {
	Name       string   `yaml:"name" json:"name"`
	Requires   []string `yaml:"requires" json:"requires"`
	RiskTags   []string `yaml:"riskTags" json:"riskTags"`
	Capability string   `yaml:"capability" json:"capability"`
}

// PackResources lists schemas and workflows bundled in the pack.
type PackResources struct {
	Schemas   []PackResource `yaml:"schemas" json:"schemas"`
	Workflows []PackResource `yaml:"workflows" json:"workflows"`
}

// PackResource identifies a single schema or workflow resource.
type PackResource struct {
	ID   string `yaml:"id" json:"id"`
	Path string `yaml:"path" json:"path"`
}

// PackOverlays groups config and policy overlays declared by the pack.
type PackOverlays struct {
	Config []PackConfigOverlay `yaml:"config" json:"config"`
	Policy []PackPolicyOverlay `yaml:"policy" json:"policy"`
}

// PackConfigOverlay describes a config patch the pack applies on install.
type PackConfigOverlay struct {
	Name     string `yaml:"name" json:"name"`
	Scope    string `yaml:"scope" json:"scope"`
	ScopeID  string `yaml:"scope_id" json:"scope_id"`
	Key      string `yaml:"key" json:"key"`
	Format   string `yaml:"format" json:"format"`
	Strategy string `yaml:"strategy" json:"strategy"`
	Path     string `yaml:"path" json:"path"`
}

// PackPolicyOverlay describes a policy fragment the pack applies on install.
type PackPolicyOverlay struct {
	Name     string `yaml:"name" json:"name"`
	Strategy string `yaml:"strategy" json:"strategy"`
	Path     string `yaml:"path" json:"path"`
}

// PackTests holds test declarations from the pack manifest.
type PackTests struct {
	PolicySimulations []PackPolicySimulation `yaml:"policySimulations" json:"policySimulations"`
}

// PackPolicySimulation describes an expected policy evaluation result.
type PackPolicySimulation struct {
	Name           string                      `yaml:"name" json:"name"`
	Request        PackPolicySimulationRequest `yaml:"request" json:"request"`
	ExpectDecision string                      `yaml:"expectDecision" json:"expectDecision"`
}

// PackPolicySimulationRequest is the input to a policy simulation test.
type PackPolicySimulationRequest struct {
	TenantId   string   `yaml:"tenantId" json:"tenantId"`
	Topic      string   `yaml:"topic" json:"topic"`
	Capability string   `yaml:"capability" json:"capability"`
	RiskTags   []string `yaml:"riskTags" json:"riskTags"`
	Requires   []string `yaml:"requires" json:"requires"`
	PackId     string   `yaml:"packId" json:"packId"`
	ActorId    string   `yaml:"actorId" json:"actorId"`
	ActorType  string   `yaml:"actorType" json:"actorType"`
}

// PackRecord is the stored state of an installed pack.
type PackRecord struct {
	ID          string              `json:"id"`
	Version     string              `json:"version"`
	Status      string              `json:"status"`
	InstalledAt string              `json:"installed_at,omitempty"`
	InstalledBy string              `json:"installed_by,omitempty"`
	Manifest    PackRecordManifest  `json:"manifest,omitempty"`
	Resources   PackRecordResources `json:"resources,omitempty"`
	Overlays    PackRecordOverlays  `json:"overlays,omitempty"`
	Tests       PackTests           `json:"tests,omitempty"`
}

// PackRecordManifest is the subset of the manifest stored in the registry.
type PackRecordManifest struct {
	Metadata      PackMetadata      `json:"metadata"`
	Compatibility PackCompatibility `json:"compatibility,omitempty"`
	Topics        []PackTopic       `json:"topics,omitempty"`
}

// PackRecordResources records the digests of installed schemas and workflows.
type PackRecordResources struct {
	Schemas   map[string]string `json:"schemas,omitempty"`
	Workflows map[string]string `json:"workflows,omitempty"`
}

// PackRecordOverlays records the config and policy overlays applied by a pack.
type PackRecordOverlays struct {
	Config []PackAppliedConfigOverlay `json:"config,omitempty"`
	Policy []PackAppliedPolicyOverlay `json:"policy,omitempty"`
}

// PackAppliedConfigOverlay records a single config overlay applied by a pack.
type PackAppliedConfigOverlay struct {
	Name    string         `json:"name"`
	Scope   string         `json:"scope"`
	ScopeID string         `json:"scope_id"`
	Key     string         `json:"key"`
	Patch   map[string]any `json:"patch"`
}

// PackAppliedPolicyOverlay records a single policy overlay applied by a pack.
type PackAppliedPolicyOverlay struct {
	Name       string `json:"name"`
	FragmentID string `json:"fragment_id"`
}

// SchemaPlan is the installation plan for a single schema resource.
type SchemaPlan struct {
	ID          string
	Schema      map[string]any
	Digest      string
	Existing    map[string]any
	HadExisting bool
	Noop        bool
}

// WorkflowPlan is the installation plan for a single workflow resource.
type WorkflowPlan struct {
	ID          string
	Workflow    map[string]any
	Digest      string
	Existing    map[string]any
	HadExisting bool
	Noop        bool
}

// AppliedConfigChange records a config overlay change with its previous value.
type AppliedConfigChange struct {
	Overlay  PackAppliedConfigOverlay
	Previous any
}

// AppliedPolicyChange records a policy overlay change with its previous value.
type AppliedPolicyChange struct {
	Overlay     PackAppliedPolicyOverlay
	Previous    any
	HadPrevious bool
}

// PackVerifyResult is the outcome of a single policy simulation test.
type PackVerifyResult struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Got      string `json:"got"`
	Reason   string `json:"reason"`
	Ok       bool   `json:"ok"`
}

// PackInstallOptions controls pack installation behavior.
type PackInstallOptions struct {
	Force       bool
	Upgrade     bool
	Inactive    bool
	Owner       string
	InstalledBy string
}

// PackInstallError wraps a pack installation error with an HTTP status code.
type PackInstallError struct {
	Status int
	Err    error
}

// Error implements the error interface.
func (e *PackInstallError) Error() string {
	if e == nil || e.Err == nil {
		return "pack install failed"
	}
	return e.Err.Error()
}
