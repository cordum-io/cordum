package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"gopkg.in/yaml.v3"
)

const (
	packRegistryScope = "system"
	packRegistryID    = "packs"

	policyConfigScope = "system"
	policyConfigID    = "policy"
	policyConfigKey   = "bundles"

	maxPackFiles             = 2048
	maxPackFileBytes         = 32 << 20
	maxPackUncompressedBytes = 256 << 20
)

type packManifest struct {
	APIVersion    string            `yaml:"apiVersion"`
	Kind          string            `yaml:"kind"`
	Metadata      packMetadata      `yaml:"metadata"`
	Compatibility packCompatibility `yaml:"compatibility"`
	Topics        []packTopic       `yaml:"topics"`
	Resources     packResources     `yaml:"resources"`
	Overlays      packOverlays      `yaml:"overlays"`
	Tests         packTests         `yaml:"tests"`
}

type packMetadata struct {
	ID          string `yaml:"id"`
	Version     string `yaml:"version"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

type packCompatibility struct {
	ProtocolVersion int    `yaml:"protocolVersion"`
	MinCoreVersion  string `yaml:"minCoreVersion"`
	MaxCoreVersion  string `yaml:"maxCoreVersion"`
}

type packTopic struct {
	Name       string   `yaml:"name"`
	Requires   []string `yaml:"requires"`
	RiskTags   []string `yaml:"riskTags"`
	Capability string   `yaml:"capability"`
}

type packResources struct {
	Schemas   []packResource `yaml:"schemas"`
	Workflows []packResource `yaml:"workflows"`
}

type packResource struct {
	ID   string `yaml:"id"`
	Path string `yaml:"path"`
}

type packOverlays struct {
	Config []packConfigOverlay `yaml:"config"`
	Policy []packPolicyOverlay `yaml:"policy"`
}

type packConfigOverlay struct {
	Name     string `yaml:"name"`
	Scope    string `yaml:"scope"`
	ScopeID  string `yaml:"scope_id"`
	Key      string `yaml:"key"`
	Format   string `yaml:"format"`
	Strategy string `yaml:"strategy"`
	Path     string `yaml:"path"`
}

type packPolicyOverlay struct {
	Name     string `yaml:"name"`
	Strategy string `yaml:"strategy"`
	Path     string `yaml:"path"`
}

type packTests struct {
	PolicySimulations []packPolicySimulation `yaml:"policySimulations"`
}

type packPolicySimulation struct {
	Name           string                      `yaml:"name"`
	Request        packPolicySimulationRequest `yaml:"request"`
	ExpectDecision string                      `yaml:"expectDecision"`
}

type packPolicySimulationRequest struct {
	TenantId   string   `yaml:"tenantId"`
	Topic      string   `yaml:"topic"`
	Capability string   `yaml:"capability"`
	RiskTags   []string `yaml:"riskTags"`
	Requires   []string `yaml:"requires"`
	PackId     string   `yaml:"packId"`
	ActorId    string   `yaml:"actorId"`
	ActorType  string   `yaml:"actorType"`
}

type packRecord struct {
	ID          string              `json:"id"`
	Version     string              `json:"version"`
	Status      string              `json:"status"`
	InstalledAt string              `json:"installed_at,omitempty"`
	InstalledBy string              `json:"installed_by,omitempty"`
	Manifest    packRecordManifest  `json:"manifest,omitempty"`
	Resources   packRecordResources `json:"resources,omitempty"`
	Overlays    packRecordOverlays  `json:"overlays,omitempty"`
	Tests       packTests           `json:"tests,omitempty"`
}

type packRecordManifest struct {
	Metadata      packMetadata      `json:"metadata"`
	Compatibility packCompatibility `json:"compatibility,omitempty"`
	Topics        []packTopic       `json:"topics,omitempty"`
}

type packRecordResources struct {
	Schemas   map[string]string `json:"schemas,omitempty"`
	Workflows map[string]string `json:"workflows,omitempty"`
}

type packRecordOverlays struct {
	Config []packAppliedConfigOverlay `json:"config,omitempty"`
	Policy []packAppliedPolicyOverlay `json:"policy,omitempty"`
}

type packAppliedConfigOverlay struct {
	Name    string         `json:"name"`
	Scope   string         `json:"scope"`
	ScopeID string         `json:"scope_id"`
	Key     string         `json:"key"`
	Patch   map[string]any `json:"patch"`
}

type packAppliedPolicyOverlay struct {
	Name       string `json:"name"`
	FragmentID string `json:"fragment_id"`
}

type packBundle struct {
	Dir     string
	cleanup func()
}

func (b *packBundle) Cleanup() {
	if b != nil && b.cleanup != nil {
		b.cleanup()
	}
}

func runPackCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	switch args[0] {
	case "install":
		runPackInstall(args[1:])
	case "uninstall":
		runPackUninstall(args[1:])
	case "list":
		runPackList(args[1:])
	case "show":
		runPackShow(args[1:])
	case "verify":
		runPackVerify(args[1:])
	default:
		usage()
		os.Exit(1)
	}
}

func runPackInstall(args []string) {
	fs := newFlagSet("pack install")
	dryRun := fs.Bool("dry-run", false, "print planned changes without writing")
	force := fs.Bool("force", false, "skip core version check")
	upgrade := fs.Bool("upgrade", false, "overwrite existing resources")
	inactive := fs.Bool("inactive", false, "install without pool mappings")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		fail("pack path or url required")
	}

	bundle, err := loadPackBundle(fs.Arg(0))
	check(err)
	defer bundle.Cleanup()

	manifest, err := loadPackManifest(bundle.Dir)
	check(err)
	if err := validatePackManifest(manifest); err != nil {
		fail(err.Error())
	}
	if err := ensureProtocolCompatible(manifest); err != nil {
		fail(err.Error())
	}
	if manifest.Compatibility.MinCoreVersion != "" && !*force {
		fail("minCoreVersion set but gateway version not available; rerun with --force to proceed")
	}

	client := newRestClient(*fs.gateway, *fs.apiKey)
	ctx := context.Background()
	owner := lockOwner()
	release, err := acquirePackLocks(ctx, client, manifest.Metadata.ID, owner)
	check(err)
	defer release()

	schemaPlans, err := planSchemas(ctx, client, bundle.Dir, manifest, *upgrade)
	check(err)
	workflowPlans, err := planWorkflows(ctx, client, bundle.Dir, manifest, *upgrade)
	check(err)

	appliedConfig := []packAppliedConfigOverlay{}
	appliedPolicy := []packAppliedPolicyOverlay{}
	appliedConfigChanges := []appliedConfigChange{}
	appliedPolicyChanges := []appliedPolicyChange{}
	appliedSchemas := []schemaPlan{}
	appliedWorkflows := []workflowPlan{}
	schemaDigests := map[string]string{}
	workflowDigests := map[string]string{}
	for _, plan := range schemaPlans {
		schemaDigests[plan.ID] = plan.Digest
	}
	for _, plan := range workflowPlans {
		workflowDigests[plan.ID] = plan.Digest
	}

	if *dryRun {
		fmt.Printf("pack %s %s: dry-run\n", manifest.Metadata.ID, manifest.Metadata.Version)
		return
	}

	rollback := func() {
		for i := len(appliedConfigChanges) - 1; i >= 0; i-- {
			_ = restoreConfigOverlay(ctx, client, appliedConfigChanges[i])
		}
		for i := len(appliedPolicyChanges) - 1; i >= 0; i-- {
			_ = restorePolicyOverlay(ctx, client, appliedPolicyChanges[i])
		}
		for i := len(appliedWorkflows) - 1; i >= 0; i-- {
			_ = rollbackWorkflow(ctx, client, appliedWorkflows[i])
		}
		for i := len(appliedSchemas) - 1; i >= 0; i-- {
			_ = rollbackSchema(ctx, client, appliedSchemas[i])
		}
	}

	installFail := func(err error) {
		rollback()
		fail(err.Error())
	}

	for _, plan := range schemaPlans {
		if plan.Noop {
			continue
		}
		if err := client.registerSchema(ctx, plan.ID, plan.Schema); err != nil {
			installFail(err)
		}
		appliedSchemas = append(appliedSchemas, plan)
	}
	for _, plan := range workflowPlans {
		if plan.Noop {
			continue
		}
		if err := client.createWorkflow(ctx, plan.Workflow); err != nil {
			installFail(err)
		}
		appliedWorkflows = append(appliedWorkflows, plan)
	}

	for _, overlay := range manifest.Overlays.Config {
		if shouldSkipConfigOverlay(*inactive, overlay) {
			continue
		}
		applied, err := applyConfigOverlay(ctx, client, overlay, manifest.Metadata.ID, bundle.Dir)
		if err != nil {
			installFail(err)
		}
		if applied.Overlay.Name != "" {
			appliedConfig = append(appliedConfig, applied.Overlay)
			appliedConfigChanges = append(appliedConfigChanges, applied)
		}
	}
	for _, overlay := range manifest.Overlays.Policy {
		applied, err := applyPolicyOverlay(ctx, client, overlay, manifest.Metadata.ID, manifest.Metadata.Version, bundle.Dir)
		if err != nil {
			installFail(err)
		}
		if applied.Overlay.Name != "" {
			appliedPolicy = append(appliedPolicy, applied.Overlay)
			appliedPolicyChanges = append(appliedPolicyChanges, applied)
		}
	}

	status := "ACTIVE"
	if *inactive || !hasPoolOverlay(appliedConfig) {
		status = "INACTIVE"
	}

	record := packRecord{
		ID:          manifest.Metadata.ID,
		Version:     manifest.Metadata.Version,
		Status:      status,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Manifest: packRecordManifest{
			Metadata:      manifest.Metadata,
			Compatibility: manifest.Compatibility,
			Topics:        manifest.Topics,
		},
		Resources: packRecordResources{
			Schemas:   schemaDigests,
			Workflows: workflowDigests,
		},
		Overlays: packRecordOverlays{
			Config: appliedConfig,
			Policy: appliedPolicy,
		},
		Tests: manifest.Tests,
	}

	check(updatePackRegistry(ctx, client, record))
	fmt.Printf("installed pack %s %s (%s)\n", record.ID, record.Version, record.Status)
}

func runPackUninstall(args []string) {
	fs := newFlagSet("pack uninstall")
	purge := fs.Bool("purge", false, "delete workflows and schemas")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		fail("pack id required")
	}
	packID := fs.Arg(0)
	client := newRestClient(*fs.gateway, *fs.apiKey)
	ctx := context.Background()
	owner := lockOwner()
	release, err := acquirePackLocks(ctx, client, packID, owner)
	check(err)
	defer release()

	record, err := getPackRecord(ctx, client, packID)
	check(err)
	if record == nil {
		fail("pack not installed")
	}

	for i := len(record.Overlays.Config) - 1; i >= 0; i-- {
		overlay := record.Overlays.Config[i]
		check(removeConfigOverlay(ctx, client, overlay))
	}
	for _, overlay := range record.Overlays.Policy {
		check(removePolicyOverlay(ctx, client, overlay))
	}
	if *purge {
		for wfID := range record.Resources.Workflows {
			_ = client.deleteWorkflow(ctx, wfID)
		}
		for schemaID := range record.Resources.Schemas {
			_ = client.deleteSchema(ctx, schemaID)
		}
	}

	record.Status = "DISABLED"
	check(updatePackRegistry(ctx, client, *record))
	fmt.Printf("uninstalled pack %s (%s)\n", record.ID, record.Status)
}

func runPackList(args []string) {
	fs := newFlagSet("pack list")
	fs.ParseArgs(args)
	client := newRestClient(*fs.gateway, *fs.apiKey)
	ctx := context.Background()
	records, err := listPackRecords(ctx, client)
	check(err)
	if len(records) == 0 {
		fmt.Println("no packs installed")
		return
	}
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rec := records[id]
		fmt.Printf("%s\t%s\t%s\n", rec.ID, rec.Version, rec.Status)
	}
}

func runPackShow(args []string) {
	fs := newFlagSet("pack show")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		fail("pack id required")
	}
	client := newRestClient(*fs.gateway, *fs.apiKey)
	ctx := context.Background()
	record, err := getPackRecord(ctx, client, fs.Arg(0))
	check(err)
	if record == nil {
		fail("pack not installed")
	}
	printJSON(record)
}

func runPackVerify(args []string) {
	fs := newFlagSet("pack verify")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		fail("pack id required")
	}
	client := newRestClient(*fs.gateway, *fs.apiKey)
	ctx := context.Background()
	record, err := getPackRecord(ctx, client, fs.Arg(0))
	check(err)
	if record == nil {
		fail("pack not installed")
	}
	if len(record.Tests.PolicySimulations) == 0 {
		fmt.Println("no policy simulations defined")
		return
	}
	for _, test := range record.Tests.PolicySimulations {
		if err := runPolicySimulation(ctx, client, test, record.ID); err != nil {
			fail(err.Error())
		}
	}
	fmt.Printf("pack %s policy simulations passed\n", record.ID)
}

type schemaPlan struct {
	ID          string
	Schema      map[string]any
	Digest      string
	Existing    map[string]any
	HadExisting bool
	Noop        bool
}

type workflowPlan struct {
	ID          string
	Workflow    map[string]any
	Digest      string
	Existing    map[string]any
	HadExisting bool
	Noop        bool
}

type appliedConfigChange struct {
	Overlay  packAppliedConfigOverlay
	Previous any
}

type appliedPolicyChange struct {
	Overlay     packAppliedPolicyOverlay
	Previous    any
	HadPrevious bool
}

func planSchemas(ctx context.Context, client *restClient, dir string, manifest *packManifest, upgrade bool) ([]schemaPlan, error) {
	plans := make([]schemaPlan, 0, len(manifest.Resources.Schemas))
	for _, ref := range manifest.Resources.Schemas {
		schemaMap, digest, err := loadSchemaFile(dir, ref.Path)
		if err != nil {
			return nil, err
		}
		plan := schemaPlan{ID: ref.ID, Schema: schemaMap, Digest: digest}
		existing, err := client.getSchema(ctx, ref.ID)
		if err != nil {
			if !isNotFound(err) {
				return nil, err
			}
		} else {
			plan.Existing = existing
			plan.HadExisting = true
			existingDigest, err := hashValue(existing)
			if err != nil {
				return nil, err
			}
			if existingDigest == digest {
				plan.Noop = true
			} else if !upgrade {
				return nil, fmt.Errorf("schema %s exists; rerun with --upgrade to overwrite", ref.ID)
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func planWorkflows(ctx context.Context, client *restClient, dir string, manifest *packManifest, upgrade bool) ([]workflowPlan, error) {
	plans := make([]workflowPlan, 0, len(manifest.Resources.Workflows))
	for _, ref := range manifest.Resources.Workflows {
		workflowMap, digest, err := loadWorkflowFile(dir, ref.Path, ref.ID)
		if err != nil {
			return nil, err
		}
		plan := workflowPlan{ID: ref.ID, Workflow: workflowMap, Digest: digest}
		existing, err := client.getWorkflow(ctx, ref.ID)
		if err != nil {
			if !isNotFound(err) {
				return nil, err
			}
		} else {
			plan.Existing = existing
			plan.HadExisting = true
			existingDigest, err := hashWorkflow(existing)
			if err != nil {
				return nil, err
			}
			if existingDigest == digest {
				plan.Noop = true
			} else if !upgrade {
				return nil, fmt.Errorf("workflow %s exists; rerun with --upgrade to overwrite", ref.ID)
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func loadPackBundle(src string) (*packBundle, error) {
	if isURL(src) {
		tmpFile, err := downloadToTemp(src)
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmpFile)
		return loadPackBundle(tmpFile)
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return &packBundle{Dir: src}, nil
	}
	if !isTarGz(src) {
		return nil, fmt.Errorf("unsupported pack format: %s", src)
	}
	tmpDir, err := os.MkdirTemp("", "cordum-pack-*")
	if err != nil {
		return nil, err
	}
	if err := extractTarGz(src, tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, err
	}
	root, err := findPackRoot(tmpDir)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, err
	}
	return &packBundle{
		Dir: root,
		cleanup: func() {
			_ = os.RemoveAll(tmpDir)
		},
	}, nil
}

func loadPackManifest(dir string) (*packManifest, error) {
	paths := []string{
		filepath.Join(dir, "pack.yaml"),
		filepath.Join(dir, "pack.yml"),
	}
	var data []byte
	var err error
	for _, path := range paths {
		// #nosec G304 -- pack manifest is read from the local pack directory.
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("pack.yaml not found: %w", err)
	}
	var manifest packManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse pack.yaml: %w", err)
	}
	return &manifest, nil
}

func validatePackManifest(manifest *packManifest) error {
	if manifest == nil {
		return errors.New("pack manifest required")
	}
	id := strings.TrimSpace(manifest.Metadata.ID)
	if id == "" {
		return errors.New("metadata.id required")
	}
	idPattern := regexp.MustCompile(`^[a-z0-9-]+$`)
	if !idPattern.MatchString(id) {
		return fmt.Errorf("metadata.id must match %s", idPattern.String())
	}
	if strings.TrimSpace(manifest.Metadata.Version) == "" {
		return errors.New("metadata.version required")
	}
	for _, topic := range manifest.Topics {
		if topic.Name == "" {
			return errors.New("topic name required")
		}
		if !strings.HasPrefix(topic.Name, "job."+id+".") {
			return fmt.Errorf("topic %q must be namespaced under job.%s.*", topic.Name, id)
		}
	}
	for _, res := range manifest.Resources.Schemas {
		if res.ID == "" || res.Path == "" {
			return errors.New("schema id and path required")
		}
		if !strings.HasPrefix(res.ID, id+"/") {
			return fmt.Errorf("schema id %q must be namespaced under %s/", res.ID, id)
		}
	}
	for _, res := range manifest.Resources.Workflows {
		if res.ID == "" || res.Path == "" {
			return errors.New("workflow id and path required")
		}
		if !strings.HasPrefix(res.ID, id+".") {
			return fmt.Errorf("workflow id %q must be namespaced under %s.", res.ID, id)
		}
	}
	return nil
}

func ensureProtocolCompatible(manifest *packManifest) error {
	if manifest.Compatibility.ProtocolVersion == 0 {
		return errors.New("compatibility.protocolVersion required")
	}
	if manifest.Compatibility.ProtocolVersion != capsdk.DefaultProtocolVersion {
		return fmt.Errorf("protocolVersion %d not supported (expected %d)", manifest.Compatibility.ProtocolVersion, capsdk.DefaultProtocolVersion)
	}
	return nil
}

func shouldSkipConfigOverlay(inactive bool, overlay packConfigOverlay) bool {
	if !inactive {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(overlay.Key), "pools")
}

func hasPoolOverlay(overlays []packAppliedConfigOverlay) bool {
	for _, overlay := range overlays {
		if strings.EqualFold(overlay.Key, "pools") {
			return true
		}
	}
	return false
}

func applyConfigOverlay(ctx context.Context, client *restClient, overlay packConfigOverlay, packID, dir string) (appliedConfigChange, error) {
	key := strings.TrimSpace(overlay.Key)
	if key == "" {
		return appliedConfigChange{}, errors.New("config overlay key required")
	}
	strategy := strings.TrimSpace(overlay.Strategy)
	if strategy != "" && strategy != "json_merge_patch" {
		return appliedConfigChange{}, fmt.Errorf("unsupported config overlay strategy %q", strategy)
	}
	patch, err := loadPatchFile(dir, overlay.Path)
	if err != nil {
		return appliedConfigChange{}, err
	}
	patchMap, ok := patch.(map[string]any)
	if !ok {
		return appliedConfigChange{}, errors.New("config overlay patch must be a map")
	}
	scope := strings.TrimSpace(overlay.Scope)
	if scope == "" {
		scope = "system"
	}
	scopeID := strings.TrimSpace(overlay.ScopeID)
	if scope == "system" && scopeID == "" {
		scopeID = "default"
	}
	doc, err := client.getConfig(ctx, scope, scopeID)
	if err != nil {
		if !isNotFound(err) {
			return appliedConfigChange{}, err
		}
		doc = &configDoc{Scope: scope, ScopeID: scopeID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	current := normalizeJSON(doc.Data[key])
	if err := validateConfigPatch(key, patchMap, packID, current); err != nil {
		return appliedConfigChange{}, err
	}
	before := deepCopy(current)
	updated := mergePatch(current, patchMap)
	doc.Data[key] = updated
	if err := client.setConfig(ctx, doc); err != nil {
		return appliedConfigChange{}, err
	}
	return appliedConfigChange{
		Overlay: packAppliedConfigOverlay{
			Name:    overlay.Name,
			Scope:   scope,
			ScopeID: scopeID,
			Key:     key,
			Patch:   patchMap,
		},
		Previous: before,
	}, nil
}

func removeConfigOverlay(ctx context.Context, client *restClient, overlay packAppliedConfigOverlay) error {
	doc, err := client.getConfig(ctx, overlay.Scope, overlay.ScopeID)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	if doc.Data == nil {
		return nil
	}
	current := normalizeJSON(doc.Data[overlay.Key])
	if current == nil {
		return nil
	}
	deletePatch := buildDeletePatch(overlay.Patch)
	updated := mergePatch(current, deletePatch)
	doc.Data[overlay.Key] = updated
	return client.setConfig(ctx, doc)
}

func restoreConfigOverlay(ctx context.Context, client *restClient, change appliedConfigChange) error {
	overlay := change.Overlay
	doc, err := client.getConfig(ctx, overlay.Scope, overlay.ScopeID)
	if err != nil {
		if !isNotFound(err) {
			return err
		}
		doc = &configDoc{Scope: overlay.Scope, ScopeID: overlay.ScopeID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	if change.Previous == nil {
		delete(doc.Data, overlay.Key)
	} else {
		doc.Data[overlay.Key] = deepCopy(change.Previous)
	}
	return client.setConfig(ctx, doc)
}

func applyPolicyOverlay(ctx context.Context, client *restClient, overlay packPolicyOverlay, packID, packVersion, dir string) (appliedPolicyChange, error) {
	strategy := strings.TrimSpace(overlay.Strategy)
	if strategy != "" && strategy != "bundle_fragment" {
		return appliedPolicyChange{}, fmt.Errorf("unsupported policy overlay strategy %q", strategy)
	}
	path, err := safeJoin(dir, overlay.Path)
	if err != nil {
		return appliedPolicyChange{}, err
	}
	// #nosec G304 -- path is validated within the pack directory.
	content, err := os.ReadFile(path)
	if err != nil {
		return appliedPolicyChange{}, err
	}
	fragmentID := policyFragmentID(packID, overlay.Name)
	doc, err := client.getConfig(ctx, policyConfigScope, policyConfigID)
	if err != nil {
		if !isNotFound(err) {
			return appliedPolicyChange{}, err
		}
		doc = &configDoc{Scope: policyConfigScope, ScopeID: policyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := normalizeJSON(doc.Data[policyConfigKey])
	bundles, _ := rawBundles.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	previous, hadPrevious := bundles[fragmentID]
	installedAt := time.Now().UTC().Format(time.RFC3339)
	sum := sha256.Sum256(content)
	bundles[fragmentID] = map[string]any{
		"content":      string(content),
		"version":      packVersion,
		"sha256":       hex.EncodeToString(sum[:]),
		"installed_at": installedAt,
	}
	doc.Data[policyConfigKey] = bundles
	if err := client.setConfig(ctx, doc); err != nil {
		return appliedPolicyChange{}, err
	}
	return appliedPolicyChange{
		Overlay: packAppliedPolicyOverlay{
			Name:       overlay.Name,
			FragmentID: fragmentID,
		},
		Previous:    deepCopy(previous),
		HadPrevious: hadPrevious,
	}, nil
}

func removePolicyOverlay(ctx context.Context, client *restClient, overlay packAppliedPolicyOverlay) error {
	doc, err := client.getConfig(ctx, policyConfigScope, policyConfigID)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	rawBundles := normalizeJSON(doc.Data[policyConfigKey])
	bundles, ok := rawBundles.(map[string]any)
	if !ok || bundles == nil {
		return nil
	}
	delete(bundles, overlay.FragmentID)
	doc.Data[policyConfigKey] = bundles
	return client.setConfig(ctx, doc)
}

func restorePolicyOverlay(ctx context.Context, client *restClient, change appliedPolicyChange) error {
	doc, err := client.getConfig(ctx, policyConfigScope, policyConfigID)
	if err != nil {
		if !isNotFound(err) {
			return err
		}
		doc = &configDoc{Scope: policyConfigScope, ScopeID: policyConfigID, Data: map[string]any{}}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	rawBundles := normalizeJSON(doc.Data[policyConfigKey])
	bundles, _ := rawBundles.(map[string]any)
	if bundles == nil {
		bundles = map[string]any{}
	}
	if !change.HadPrevious {
		delete(bundles, change.Overlay.FragmentID)
	} else {
		bundles[change.Overlay.FragmentID] = deepCopy(change.Previous)
	}
	doc.Data[policyConfigKey] = bundles
	return client.setConfig(ctx, doc)
}

func policyFragmentID(packID, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	return fmt.Sprintf("%s/%s", packID, name)
}

func rollbackSchema(ctx context.Context, client *restClient, plan schemaPlan) error {
	if plan.HadExisting && plan.Existing != nil {
		return client.registerSchema(ctx, plan.ID, plan.Existing)
	}
	return client.deleteSchema(ctx, plan.ID)
}

func rollbackWorkflow(ctx context.Context, client *restClient, plan workflowPlan) error {
	if plan.HadExisting && plan.Existing != nil {
		return client.createWorkflow(ctx, plan.Existing)
	}
	return client.deleteWorkflow(ctx, plan.ID)
}

func runPolicySimulation(ctx context.Context, client *restClient, test packPolicySimulation, packID string) error {
	if test.Request.Topic == "" {
		return fmt.Errorf("policy simulation %q missing topic", test.Name)
	}
	request := map[string]any{
		"topic":  test.Request.Topic,
		"tenant": test.Request.TenantId,
		"meta": map[string]any{
			"tenant_id":  test.Request.TenantId,
			"capability": test.Request.Capability,
			"risk_tags":  test.Request.RiskTags,
			"requires":   test.Request.Requires,
			"pack_id":    test.Request.PackId,
			"actor_id":   test.Request.ActorId,
			"actor_type": test.Request.ActorType,
		},
	}
	if requestMeta, ok := request["meta"].(map[string]any); ok {
		if requestMeta["pack_id"] == "" {
			requestMeta["pack_id"] = packID
		}
		if requestMeta["tenant_id"] == "" {
			requestMeta["tenant_id"] = "default"
		}
	}
	var resp struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/api/v1/policy/simulate", request, &resp); err != nil {
		return err
	}
	got := normalizeDecision(resp.Decision)
	expect := normalizeDecision(test.ExpectDecision)
	if got != expect {
		return fmt.Errorf("policy simulation %q expected %s, got %s (%s)", test.Name, expect, got, resp.Reason)
	}
	return nil
}

func normalizeDecision(raw string) string {
	val := strings.ToUpper(strings.TrimSpace(raw))
	switch val {
	case "DECISION_TYPE_ALLOW", "ALLOW":
		return "ALLOW"
	case "DECISION_TYPE_DENY", "DENY":
		return "DENY"
	case "DECISION_TYPE_REQUIRE_HUMAN", "REQUIRE_APPROVAL", "REQUIRE_HUMAN":
		return "REQUIRE_APPROVAL"
	case "DECISION_TYPE_ALLOW_WITH_CONSTRAINTS", "ALLOW_WITH_CONSTRAINTS":
		return "ALLOW_WITH_CONSTRAINTS"
	case "DECISION_TYPE_THROTTLE", "THROTTLE":
		return "THROTTLE"
	default:
		return val
	}
}

func updatePackRegistry(ctx context.Context, client *restClient, record packRecord) error {
	records, doc, err := loadPackRegistry(ctx, client)
	if err != nil {
		return err
	}
	records[record.ID] = record
	if doc == nil {
		doc = &configDoc{Scope: packRegistryScope, ScopeID: packRegistryID, Data: map[string]any{}}
	}
	doc.Data["installed"] = recordsToAny(records)
	return client.setConfig(ctx, doc)
}

func listPackRecords(ctx context.Context, client *restClient) (map[string]packRecord, error) {
	records, _, err := loadPackRegistry(ctx, client)
	return records, err
}

func getPackRecord(ctx context.Context, client *restClient, packID string) (*packRecord, error) {
	records, _, err := loadPackRegistry(ctx, client)
	if err != nil {
		return nil, err
	}
	rec, ok := records[packID]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

func loadPackRegistry(ctx context.Context, client *restClient) (map[string]packRecord, *configDoc, error) {
	doc, err := client.getConfig(ctx, packRegistryScope, packRegistryID)
	if err != nil {
		if isNotFound(err) {
			return map[string]packRecord{}, nil, nil
		}
		return nil, nil, err
	}
	records := map[string]packRecord{}
	raw := normalizeJSON(doc.Data["installed"])
	if raw == nil {
		return records, doc, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, nil, err
	}
	return records, doc, nil
}

func recordsToAny(records map[string]packRecord) map[string]any {
	data, err := json.Marshal(records)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func acquirePackLocks(ctx context.Context, client *restClient, packID, owner string) (func(), error) {
	global := "packs:global"
	if err := client.acquireLock(ctx, global, owner, 60*time.Second); err != nil {
		return func() {}, err
	}
	packLock := "pack:" + packID
	if err := client.acquireLock(ctx, packLock, owner, 60*time.Second); err != nil {
		_ = client.releaseLock(ctx, global, owner)
		return func() {}, err
	}
	return func() {
		_ = client.releaseLock(ctx, packLock, owner)
		_ = client.releaseLock(ctx, global, owner)
	}, nil
}

func lockOwner() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "local"
	}
	return fmt.Sprintf("cordumctl:%s:%d", host, os.Getpid())
}

func loadSchemaFile(dir, relPath string) (map[string]any, string, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, "", err
	}
	payload, err := loadDataFile(path)
	if err != nil {
		return nil, "", err
	}
	schemaMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("schema file must be an object")
	}
	digest, err := hashValue(schemaMap)
	if err != nil {
		return nil, "", err
	}
	return schemaMap, digest, nil
}

func loadWorkflowFile(dir, relPath, id string) (map[string]any, string, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, "", err
	}
	payload, err := loadDataFile(path)
	if err != nil {
		return nil, "", err
	}
	workflowMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("workflow file must be an object")
	}
	if id != "" {
		workflowMap["id"] = id
	}
	normalized := normalizeWorkflowMap(workflowMap)
	digest, err := hashValue(normalized)
	if err != nil {
		return nil, "", err
	}
	return workflowMap, digest, nil
}

func normalizeWorkflowMap(workflow map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range workflow {
		switch k {
		case "created_at", "updated_at":
			continue
		default:
			out[k] = v
		}
	}
	return out
}

func hashWorkflow(workflow map[string]any) (string, error) {
	return hashValue(normalizeWorkflowMap(workflow))
}

func loadDataFile(path string) (any, error) {
	// #nosec G304 -- path is validated by safeJoin at call sites.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload any
	if json.Unmarshal(data, &payload) == nil {
		return normalizeJSON(payload), nil
	}
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return normalizeJSON(payload), nil
}

func loadPatchFile(dir, relPath string) (any, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, err
	}
	return loadDataFile(path)
}

func validateConfigPatch(key string, patch map[string]any, packID string, current any) error {
	switch strings.ToLower(key) {
	case "pools":
		return validatePoolsPatch(patch, packID, current)
	case "timeouts":
		return validateTimeoutsPatch(patch, packID)
	default:
		return fmt.Errorf("unsupported config overlay key %q", key)
	}
}

func validatePoolsPatch(patch map[string]any, packID string, current any) error {
	rawTopics := normalizeJSON(patch["topics"])
	if rawTopics != nil {
		topics, ok := rawTopics.(map[string]any)
		if !ok {
			return errors.New("pools.topics must be a map")
		}
		for topic := range topics {
			if !strings.HasPrefix(topic, "job."+packID+".") {
				return fmt.Errorf("pools topic %q must be namespaced under job.%s.*", topic, packID)
			}
		}
	}
	rawPools := normalizeJSON(patch["pools"])
	if rawPools != nil {
		pools, ok := rawPools.(map[string]any)
		if !ok {
			return errors.New("pools.pools must be a map")
		}
		existingPools := extractPools(current)
		for pool := range pools {
			if _, ok := existingPools[pool]; ok {
				continue
			}
			if !strings.HasPrefix(pool, packID) {
				return fmt.Errorf("pool %q must be namespaced under %s", pool, packID)
			}
		}
	}
	for key := range patch {
		if key != "topics" && key != "pools" {
			return fmt.Errorf("unsupported pools overlay key %q", key)
		}
	}
	return nil
}

func extractPools(current any) map[string]struct{} {
	out := map[string]struct{}{}
	currentMap, ok := normalizeJSON(current).(map[string]any)
	if !ok || currentMap == nil {
		return out
	}
	rawPools := normalizeJSON(currentMap["pools"])
	pools, ok := rawPools.(map[string]any)
	if !ok || pools == nil {
		return out
	}
	for name := range pools {
		out[name] = struct{}{}
	}
	return out
}

func validateTimeoutsPatch(patch map[string]any, packID string) error {
	rawTopics := normalizeJSON(patch["topics"])
	if rawTopics != nil {
		topics, ok := rawTopics.(map[string]any)
		if !ok {
			return errors.New("timeouts.topics must be a map")
		}
		for topic := range topics {
			if !strings.HasPrefix(topic, "job."+packID+".") {
				return fmt.Errorf("timeouts topic %q must be namespaced under job.%s.*", topic, packID)
			}
		}
	}
	rawWorkflows := normalizeJSON(patch["workflows"])
	if rawWorkflows != nil {
		workflows, ok := rawWorkflows.(map[string]any)
		if !ok {
			return errors.New("timeouts.workflows must be a map")
		}
		for wf := range workflows {
			if !strings.HasPrefix(wf, packID+".") {
				return fmt.Errorf("timeout workflow %q must be namespaced under %s.", wf, packID)
			}
		}
	}
	for key := range patch {
		if key != "topics" && key != "workflows" {
			return fmt.Errorf("unsupported timeouts overlay key %q", key)
		}
	}
	return nil
}

func buildDeletePatch(patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range patch {
		out[k] = deletePatchValue(v)
	}
	return out
}

func deletePatchValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			out[k] = deletePatchValue(child)
		}
		return out
	default:
		return nil
	}
}

func mergePatch(target any, patch any) any {
	if patch == nil {
		return nil
	}
	patchMap, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	targetMap, _ := normalizeJSON(target).(map[string]any)
	if targetMap == nil {
		targetMap = map[string]any{}
	} else {
		targetMap = cloneMap(targetMap)
	}
	for k, v := range patchMap {
		if v == nil {
			delete(targetMap, k)
			continue
		}
		if childMap, ok := v.(map[string]any); ok {
			targetMap[k] = mergePatch(targetMap[k], childMap)
			continue
		}
		targetMap[k] = v
	}
	return targetMap
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			out[k] = normalizeJSON(child)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, child := range v {
			key := fmt.Sprint(k)
			out[key] = normalizeJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = normalizeJSON(child)
		}
		return out
	default:
		return v
	}
}

func deepCopy(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}

func hashValue(value any) (string, error) {
	encoded, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalJSON(value any) ([]byte, error) {
	buf := &strings.Builder{}
	if err := appendCanonical(buf, value); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

func appendCanonical(buf *strings.Builder, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case map[string]any:
		return appendCanonicalMap(buf, v)
	case []any:
		return appendCanonicalSlice(buf, v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(encoded)
		return nil
	}
}

func appendCanonicalMap(buf *strings.Builder, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(k)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		if err := appendCanonical(buf, m[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

func appendCanonicalSlice(buf *strings.Builder, items []any) error {
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := appendCanonical(buf, item); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

type restClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newRestClient(gateway, apiKey string) *restClient {
	return &restClient{
		baseURL: strings.TrimRight(gateway, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *restClient) endpoint(path string) string {
	return c.baseURL + path
}

type httpError struct {
	Status  int
	Message string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("status %d: %s", e.Status, e.Message)
}

func isNotFound(err error) bool {
	var httpErr *httpError
	return errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound
}

func (c *restClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		buf := &strings.Builder{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return err
		}
		payload = strings.NewReader(buf.String())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = resp.Status
		}
		return &httpError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type configDoc struct {
	Scope    string            `json:"scope"`
	ScopeID  string            `json:"scope_id"`
	Data     map[string]any    `json:"data"`
	Revision int64             `json:"revision"`
	Updated  string            `json:"updated_at"`
	Meta     map[string]string `json:"meta,omitempty"`
}

func (c *restClient) getConfig(ctx context.Context, scope, scopeID string) (*configDoc, error) {
	path := "/api/v1/config?scope=" + url.QueryEscape(scope) + "&scope_id=" + url.QueryEscape(scopeID)
	var doc configDoc
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (c *restClient) setConfig(ctx context.Context, doc *configDoc) error {
	if doc == nil {
		return errors.New("config doc required")
	}
	req := map[string]any{
		"scope":    doc.Scope,
		"scope_id": doc.ScopeID,
		"data":     doc.Data,
		"meta":     doc.Meta,
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/config", req, nil)
}

func (c *restClient) getSchema(ctx context.Context, id string) (map[string]any, error) {
	var resp struct {
		Schema map[string]any `json:"schema"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/schemas/"+id, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Schema, nil
}

func (c *restClient) registerSchema(ctx context.Context, id string, schema map[string]any) error {
	req := map[string]any{
		"id":     id,
		"schema": schema,
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/schemas", req, nil)
}

func (c *restClient) deleteSchema(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/schemas/"+id, nil, nil)
}

func (c *restClient) getWorkflow(ctx context.Context, id string) (map[string]any, error) {
	var resp map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workflows/"+id, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *restClient) createWorkflow(ctx context.Context, workflow map[string]any) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/workflows", workflow, nil)
}

func (c *restClient) deleteWorkflow(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/workflows/"+id, nil, nil)
}

func (c *restClient) acquireLock(ctx context.Context, resource, owner string, ttl time.Duration) error {
	req := map[string]any{
		"resource": resource,
		"owner":    owner,
		"mode":     "exclusive",
		"ttl_ms":   ttl.Milliseconds(),
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/locks/acquire", req, nil)
}

func (c *restClient) releaseLock(ctx context.Context, resource, owner string) error {
	req := map[string]any{
		"resource": resource,
		"owner":    owner,
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/locks/release", req, nil)
}

func isURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func downloadToTemp(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("url host required")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	// #nosec G107 -- URL is operator-provided and validated.
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	tmpFile, err := os.CreateTemp("", "cordum-pack-*.tgz")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return "", err
	}
	return tmpFile.Name(), nil
}

func isTarGz(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz")
}

func extractTarGz(path, dest string) error {
	// #nosec G304 -- pack archive path is provided by the local operator.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var (
		files   int
		totalSz int64
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			files++
			if files > maxPackFiles {
				return fmt.Errorf("pack archive exceeds max files (%d)", maxPackFiles)
			}
			if hdr.Size < 0 || hdr.Size > maxPackFileBytes {
				return fmt.Errorf("pack file too large: %s", hdr.Name)
			}
			totalSz += hdr.Size
			if totalSz > maxPackUncompressedBytes {
				return fmt.Errorf("pack archive exceeds max size (%d bytes)", maxPackUncompressedBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			// #nosec G304 -- target path is validated by safeJoin.
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(out, tr, hdr.Size); err != nil && !errors.Is(err, io.EOF) {
				if cerr := out.Close(); cerr != nil {
					return cerr
				}
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(name))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute archive path: %s", name)
	}
	target := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid archive path: %s", name)
	}
	return target, nil
}

func findPackRoot(dir string) (string, error) {
	if exists(filepath.Join(dir, "pack.yaml")) || exists(filepath.Join(dir, "pack.yml")) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return "", errors.New("pack.yaml not found in archive root")
	}
	subdir := filepath.Join(dir, entries[0].Name())
	if exists(filepath.Join(subdir, "pack.yaml")) || exists(filepath.Join(subdir, "pack.yml")) {
		return subdir, nil
	}
	return "", errors.New("pack.yaml not found in archive")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
