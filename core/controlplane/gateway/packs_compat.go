package gateway

// packs_compat.go provides backward-compatible aliases so that all gateway
// handler methods and tests continue to compile after pack types, constants,
// validation functions, and marketplace utilities moved to the packs/
// sub-package.

import (
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
)

// ---------- type aliases ----------

type packManifest = packs.PackManifest
type packMetadata = packs.PackMetadata
type packCompatibility = packs.PackCompatibility
type packTopic = packs.PackTopic
type packResources = packs.PackResources
type packResource = packs.PackResource
type packOverlays = packs.PackOverlays
type packConfigOverlay = packs.PackConfigOverlay
type packPolicyOverlay = packs.PackPolicyOverlay
type packTests = packs.PackTests
type packPolicySimulation = packs.PackPolicySimulation
type packPolicySimulationRequest = packs.PackPolicySimulationRequest
type packRecord = packs.PackRecord
type packRecordManifest = packs.PackRecordManifest
type packRecordResources = packs.PackRecordResources
type packRecordOverlays = packs.PackRecordOverlays
type packAppliedConfigOverlay = packs.PackAppliedConfigOverlay
type packAppliedPolicyOverlay = packs.PackAppliedPolicyOverlay
type schemaPlan = packs.SchemaPlan
type workflowPlan = packs.WorkflowPlan
type appliedConfigChange = packs.AppliedConfigChange
type appliedPolicyChange = packs.AppliedPolicyChange
type packVerifyResult = packs.PackVerifyResult
type packInstallOptions = packs.PackInstallOptions
type packInstallError = packs.PackInstallError

type marketplaceCatalogConfig = packs.MarketplaceCatalogConfig
type marketplaceCatalog = packs.MarketplaceCatalog
type marketplaceCatalogFile = packs.MarketplaceCatalogFile
type marketplaceCatalogPack = packs.MarketplaceCatalogPack
type marketplaceCatalogStatus = packs.MarketplaceCatalogStatus
type marketplacePackItem = packs.MarketplacePackItem
type marketplaceResponse = packs.MarketplaceResponse
type marketplaceCache = packs.MarketplaceCache
type marketplaceCatalogEntry = packs.MarketplaceCatalogEntry
type marketplaceInstallRequest = packs.MarketplaceInstallRequest

// ---------- constant aliases ----------

const (
	packRegistryScope = packs.PackRegistryScope
	packRegistryID    = packs.PackRegistryID

	packCatalogScope        = packs.PackCatalogScope
	packCatalogID           = packs.PackCatalogID
	defaultPackCatalogID    = packs.DefaultPackCatalogID
	defaultPackCatalogTitle = packs.DefaultPackCatalogTitle
	defaultPackCatalogURL   = packs.DefaultPackCatalogURL

	envPackCatalogID             = packs.EnvPackCatalogID
	envPackCatalogTitle          = packs.EnvPackCatalogTitle
	envPackCatalogURL            = packs.EnvPackCatalogURL
	envPackCatalogDisableDefault = packs.EnvPackCatalogDisableDefault
	envMarketplaceAllowHTTP      = packs.EnvMarketplaceAllowHTTP
	envMarketplaceHTTPTimeout    = packs.EnvMarketplaceHTTPTimeout

	policyConfigScope = packs.PolicyConfigScope
	policyConfigID    = packs.PolicyConfigID
	policyConfigKey   = packs.PolicyConfigKey

	maxPackUploadBytes       = packs.MaxPackUploadBytes
	maxPackFiles             = packs.MaxPackFiles
	maxPackFileBytes         = packs.MaxPackFileBytes
	maxPackUncompressedBytes = packs.MaxPackUncompressedBytes
	maxCatalogBytes          = packs.MaxCatalogBytes
)

// Time-based constants cannot use const alias; use var.
var (
	marketplaceCacheTTL           = packs.MarketplaceCacheTTL
	defaultMarketplaceHTTPTimeout = packs.DefaultMarketplaceHTTPTimeout
)

// ---------- function re-exports (validate.go) ----------

var (
	loadPackBundleFromReader    = packs.LoadPackBundleFromReader
	loadPackManifest            = packs.LoadPackManifest
	validatePackManifest        = packs.ValidatePackManifest
	ensureProtocolCompatible    = packs.EnsureProtocolCompatible
	ensureCoreVersionCompatible = packs.EnsureCoreVersionCompatible
	parseSemver                 = packs.ParseSemver
	compareSemver               = packs.CompareSemver
	shouldSkipConfigOverlay     = packs.ShouldSkipConfigOverlay
	hasPoolOverlay              = packs.HasPoolOverlay
	validateConfigPatch         = packs.ValidateConfigPatch
	validatePoolsPatch          = packs.ValidatePoolsPatch
	extractPools                = packs.ExtractPools
	validateTimeoutsPatch       = packs.ValidateTimeoutsPatch
	loadSchemaFile              = packs.LoadSchemaFile
	loadWorkflowFile            = packs.LoadWorkflowFile
	normalizeWorkflowMap        = packs.NormalizeWorkflowMap
	hashWorkflow                = packs.HashWorkflow
	workflowToMap               = packs.WorkflowToMap
	loadDataFile                = packs.LoadDataFile
	loadPatchFile               = packs.LoadPatchFile
	normalizeJSON               = packs.NormalizeJSON
	deepCopy                    = packs.DeepCopy
	mergePatch                  = packs.MergePatch
	buildDeletePatch            = packs.BuildDeletePatch
	hashValue                   = packs.HashValue
	canonicalJSON               = packs.CanonicalJSON
	appendCanonical             = packs.AppendCanonical
	isTarGz                     = packs.IsTarGz
	exists                      = packs.Exists
	findPackRoot                = packs.FindPackRoot
	extractTarGzReader          = packs.ExtractTarGzReader
	safeJoin                    = packs.SafeJoin
)

// ---------- function re-exports (marketplace.go) ----------

var (
	seedDefaultPackCatalogs = packs.SeedDefaultPackCatalogs
	compareVersions         = packs.CompareVersions
	normalizeVersion        = packs.NormalizeVersion
	parseVersion            = packs.ParseVersion
	resolvePackURL          = packs.ResolvePackURL
	hostFromURL             = packs.HostFromURL
	cloneMarketplaceResponse = packs.CloneMarketplaceResponse
	isPrivateNet            = packs.IsPrivateNet
)

// ---------- var re-exports ----------

var (
	errMarketplaceNotFound         = packs.ErrMarketplaceNotFound
	marketplaceCatalogFetchTimeout = packs.MarketplaceCatalogFetchTimeout
	privateIPNets                  = packs.PrivateIPNets
	privateHostnames               = packs.PrivateHostnames
)
