package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	sdk "github.com/cordum/cordum/sdk/client"
	"gopkg.in/yaml.v3"
)

const (
	packRegistryScope = "system"
	packRegistryID    = "packs"

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
	// Aliases declares additional namespace identifiers this pack owns,
	// in addition to ID. When set, topic/pools-patch namespace checks
	// accept `job.<id>.*` AND `job.<alias>.*` for each alias. Each alias
	// must match `^[a-z][a-z0-9_-]{1,30}$`; max 8 entries. Existing packs
	// that omit this field keep validating under the strict prefix rule.
	Aliases []string `yaml:"aliases,omitempty"`
}

type packCompatibility struct {
	ProtocolVersion int    `yaml:"protocolVersion"`
	MinCoreVersion  string `yaml:"minCoreVersion"`
	MaxCoreVersion  string `yaml:"maxCoreVersion"`
}

type packTopic struct {
	Name           string   `yaml:"name"`
	Requires       []string `yaml:"requires"`
	RiskTags       []string `yaml:"riskTags"`
	Capability     string   `yaml:"capability"`
	InputSchemaID  string   `yaml:"inputSchema,omitempty" json:"input_schema_id,omitempty"`
	OutputSchemaID string   `yaml:"outputSchema,omitempty" json:"output_schema_id,omitempty"`
}

type topicRegistration struct {
	Name           string   `json:"name"`
	Pool           string   `json:"pool,omitempty"`
	InputSchemaID  string   `json:"input_schema_id,omitempty"`
	OutputSchemaID string   `json:"output_schema_id,omitempty"`
	PackID         string   `json:"pack_id,omitempty"`
	Requires       []string `json:"requires,omitempty"`
	RiskTags       []string `json:"risk_tags,omitempty"`
	Status         string   `json:"status"`
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
	PolicySimulations []packPolicySimulation `yaml:"policySimulations" json:"policySimulations"`
}

type packPolicySimulation struct {
	Name           string                      `yaml:"name" json:"name"`
	Request        packPolicySimulationRequest `yaml:"request" json:"request"`
	ExpectDecision string                      `yaml:"expectDecision" json:"expectDecision"`
}

type packPolicySimulationRequest struct {
	TenantId   string   `yaml:"tenantId" json:"tenantId"`
	Topic      string   `yaml:"topic" json:"topic"`
	Capability string   `yaml:"capability" json:"capability"`
	RiskTags   []string `yaml:"riskTags" json:"riskTags"`
	Requires   []string `yaml:"requires" json:"requires"`
	PackId     string   `yaml:"packId" json:"packId"`
	ActorId    string   `yaml:"actorId" json:"actorId"`
	ActorType  string   `yaml:"actorType" json:"actorType"`
}

type packRecord struct {
	ID           string                  `json:"id"`
	Version      string                  `json:"version"`
	Status       string                  `json:"status"`
	InstalledAt  string                  `json:"installed_at,omitempty"`
	InstalledBy  string                  `json:"installed_by,omitempty"`
	Manifest     packRecordManifest      `json:"manifest,omitempty"`
	Resources    packRecordResources     `json:"resources,omitempty"`
	Overlays     packRecordOverlays      `json:"overlays,omitempty"`
	Tests        packTests               `json:"tests,omitempty"`
	Verification *packRecordVerification `json:"verification,omitempty"`
}

// packRecordVerification is the gateway's verification result for an installed pack.
type packRecordVerification struct {
	Signed              bool     `json:"signed"`
	PublisherID         string   `json:"publisher_id,omitempty"`
	KID                 string   `json:"kid,omitempty"`
	VerifiedAt          string   `json:"verified_at,omitempty"`
	HasCordumCounterSig bool     `json:"has_cordum_counter_sig,omitempty"`
	SignatureAlgorithm  string   `json:"signature_algorithm,omitempty"`
	PackSignatureVer    int      `json:"pack_signature_version,omitempty"`
	Warnings            []string `json:"warnings,omitempty"`
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

type packInstallResult struct {
	packRecord
	WorkerCredential *struct {
		WorkerID      string   `json:"worker_id"`
		Token         string   `json:"token"`
		AllowedPools  []string `json:"allowed_pools"`
		AllowedTopics []string `json:"allowed_topics"`
	} `json:"worker_credential"`
}

func packArchive(dir string) ([]byte, error) {
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	files, total := 0, int64(0)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported pack entry %s", path)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		files++
		if !info.IsDir() {
			total += info.Size()
		}
		if files > maxPackFiles || info.Size() > maxPackFileBytes || total > maxPackUncompressedBytes {
			return fmt.Errorf("pack archive limit exceeded at %s", path)
		}
		return writePackArchiveEntry(tw, path, filepath.ToSlash(rel), info)
	})
	if err := errors.Join(err, tw.Close(), gz.Close()); err != nil {
		return nil, fmt.Errorf("archive pack: %w", err)
	}
	return body.Bytes(), nil
}

func writePackArchiveEntry(tw *tar.Writer, path, name string, info os.FileInfo) error {
	header := &tar.Header{Name: name, Mode: int64(info.Mode().Perm()), Typeflag: tar.TypeDir}
	if info.IsDir() {
		header.Name += "/"
		return tw.WriteHeader(header)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	current, err := file.Stat()
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return fmt.Errorf("pack entry changed: %s", path)
	}
	header.Typeflag, header.Size = tar.TypeReg, info.Size()
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err = io.Copy(tw, io.LimitReader(file, maxPackFileBytes+1))
	return err
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
	// Pack-signing subcommands (task-6ced7932) — keygen, sign,
	// verify-signature, export-key — share dispatch via
	// dispatchPackSigningCmd in pack_sign.go. They don't collide with
	// the legacy install/verify commands because each uses a distinct
	// verb.
	if handled, err := dispatchPackSigningCmd(args[0], args[1:]); handled {
		if err != nil {
			fail(err.Error())
		}
		return
	}
	switch args[0] {
	case "create":
		runPackCreate(args[1:])
	case "install":
		if err := runPackInstall(args[1:]); err != nil {
			fail(err.Error())
		}
	case "uninstall":
		if err := runPackUninstall(args[1:]); err != nil {
			fail(err.Error())
		}
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

func runPackInstall(args []string) error {
	fs := newFlagSet("pack install")
	dryRun := fs.Bool("dry-run", false, "print planned changes without writing")
	force := fs.Bool("force", false, "skip core version check and allow --no-verify in strict mode")
	upgrade := fs.Bool("upgrade", false, "overwrite existing resources")
	inactive := fs.Bool("inactive", false, "install without pool mappings")
	strict := fs.Bool("strict", false, "refuse to install unsigned packs (also: CORDUM_PACK_STRICT=true)")
	requireCordumSig := fs.Bool("require-cordum-sig", false, "require Cordum counter-signature in addition to publisher signature")
	trustedKeys := fs.String("trusted-keys", "", "directory of trusted publisher public keys (*.pub)")
	noVerify := fs.Bool("no-verify", false, "skip only client signature check; gateway still verifies (requires --force in strict mode)")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		return errors.New("pack path or url required")
	}

	tlsTransport, tlsErr := sdk.BuildTLSTransportErr(fs.tlsOptions())
	if tlsErr != nil {
		return fmt.Errorf("tls configuration error: %w", tlsErr)
	}

	bundle, err := loadPackBundle(fs.Arg(0), tlsTransport)
	if err != nil {
		return err
	}
	defer bundle.Cleanup()

	manifest, err := loadPackManifest(bundle.Dir)
	if err != nil {
		return err
	}
	if err := validatePackManifest(manifest); err != nil {
		return err
	}
	if err := ensureProtocolCompatible(manifest); err != nil {
		return err
	}

	// Client verification is an early check; the gateway remains authoritative.
	_, err = VerifyInstallBundle(bundle.Dir, manifest.Metadata.ID, PackVerificationOptions{
		Strict:           resolvePackStrict(*strict),
		RequireCordumSig: *requireCordumSig,
		NoVerify:         *noVerify,
		Force:            *force,
		TrustedKeysDir:   *trustedKeys,
		Stderr:           os.Stderr,
	})
	if err != nil {
		return err
	}

	client := newRestClient(*fs.gateway, *fs.apiKey, *fs.tenant, tlsTransport)
	ctx := context.Background()
	if !*force {
		if err := enforceCoreVersion(ctx, client, manifest); err != nil {
			return err
		}
	}
	if *dryRun {
		if _, err := planSchemas(ctx, client, bundle.Dir, manifest, *upgrade); err != nil {
			return err
		}
		if _, err := planWorkflows(ctx, client, bundle.Dir, manifest, *upgrade); err != nil {
			return err
		}
		fmt.Printf("pack %s %s: dry-run\n", manifest.Metadata.ID, manifest.Metadata.Version)
		return nil
	}
	archive, err := packArchive(bundle.Dir)
	if err != nil {
		return err
	}
	result, err := client.installPack(ctx, archive, *force, *upgrade, *inactive)
	if err != nil {
		return err
	}
	fmt.Printf("installed pack %s %s (%s)\n", result.ID, result.Version, result.Status)
	if credential := result.WorkerCredential; credential != nil {
		fmt.Printf("worker_id: %s\nworker_token: %s\n", credential.WorkerID, credential.Token)
	}
	return nil
}

func runPackUninstall(args []string) error {
	fs := newFlagSet("pack uninstall")
	purge := fs.Bool("purge", false, "delete workflows and schemas")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		return errors.New("pack id required")
	}
	packID := fs.Arg(0)
	tlsTransport, tlsErr := sdk.BuildTLSTransportErr(fs.tlsOptions())
	if tlsErr != nil {
		return fmt.Errorf("tls configuration error: %w", tlsErr)
	}
	client := newRestClient(*fs.gateway, *fs.apiKey, *fs.tenant, tlsTransport)
	ctx := context.Background()
	if err := client.uninstallPack(ctx, packID, *purge); err != nil {
		return err
	}
	fmt.Printf("uninstalled pack %s\n", packID)
	return nil
}

func restClientFromFlags(fs *flagSet) *restClient {
	tlsTransport, tlsErr := sdk.BuildTLSTransportErr(fs.tlsOptions())
	if tlsErr != nil {
		fail(fmt.Sprintf("tls configuration error: %v", tlsErr))
	}
	return newRestClient(*fs.gateway, *fs.apiKey, *fs.tenant, tlsTransport)
}

func runPackList(args []string) {
	fs := newFlagSet("pack list")
	fs.ParseArgs(args)
	client := restClientFromFlags(fs)
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
	client := restClientFromFlags(fs)
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
	client := restClientFromFlags(fs)
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

func planSchemas(ctx context.Context, client *restClient, dir string, manifest *packManifest, upgrade bool) ([]schemaPlan, error) {
	plans := make([]schemaPlan, 0, len(manifest.Resources.Schemas))
	for _, ref := range manifest.Resources.Schemas {
		schemaMap, digest, err := loadSchemaFile(dir, ref.Path)
		if err != nil {
			return nil, fmt.Errorf("plan schema %s: %w", ref.ID, err)
		}
		plan := schemaPlan{ID: ref.ID, Schema: schemaMap, Digest: digest}
		existing, err := client.getSchema(ctx, ref.ID)
		if err != nil {
			if !isNotFound(err) {
				return nil, fmt.Errorf("plan schema %s: %w", ref.ID, err)
			}
		} else {
			plan.Existing = existing
			plan.HadExisting = true
			existingDigest, err := hashValue(existing)
			if err != nil {
				return nil, fmt.Errorf("plan schema %s: %w", ref.ID, err)
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
			return nil, fmt.Errorf("plan workflow %s: %w", ref.ID, err)
		}
		plan := workflowPlan{ID: ref.ID, Workflow: workflowMap, Digest: digest}
		existing, err := client.getWorkflow(ctx, ref.ID)
		if err != nil {
			if !isNotFound(err) {
				return nil, fmt.Errorf("plan workflow %s: %w", ref.ID, err)
			}
		} else {
			plan.Existing = existing
			plan.HadExisting = true
			existingDigest, err := hashWorkflow(existing)
			if err != nil {
				return nil, fmt.Errorf("plan workflow %s: %w", ref.ID, err)
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

func loadPackBundle(src string, transport *http.Transport) (*packBundle, error) {
	if isURL(src) {
		tmpFile, err := downloadToTemp(src, transport)
		if err != nil {
			return nil, fmt.Errorf("load pack bundle: %w", err)
		}
		defer func() { _ = os.Remove(tmpFile) }()
		return loadPackBundle(tmpFile, transport)
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("load pack bundle: %w", err)
	}
	if info.IsDir() {
		return &packBundle{Dir: src}, nil
	}
	if !isTarGz(src) {
		return nil, fmt.Errorf("unsupported pack format: %s", src)
	}
	tmpDir, err := os.MkdirTemp("", "cordum-pack-*")
	if err != nil {
		return nil, fmt.Errorf("load pack bundle: %w", err)
	}
	if err := extractTarGz(src, tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("load pack bundle: %w", err)
	}
	root, err := findPackRoot(tmpDir)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("load pack bundle: %w", err)
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

// packAliasPattern bounds the alias namespace: leading lower-case letter,
// followed by lower-case alnum / hyphen / underscore, up to 31 chars total.
// Tight on purpose — prevents namespace squatting by forcing distinct,
// auditable identifiers per alias declaration.
var packAliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,30}$`)

// maxPackAliases caps the number of alias entries a single pack may
// declare. Eight is enough for legitimate "this pack owns sibling
// namespaces" cases (e.g., cordclaw → openclaw) without enabling broad
// namespace fan-out.
const maxPackAliases = 8

// validatePackAliases enforces the alias regex + cap. Returns nil when
// the slice is empty; called from validatePackManifest before any topic
// or pool prefix check so an alias typo fails install-time, not
// emit-time.
func validatePackAliases(aliases []string) error {
	if len(aliases) == 0 {
		return nil
	}
	if len(aliases) > maxPackAliases {
		return fmt.Errorf("metadata.aliases: at most %d entries allowed, got %d", maxPackAliases, len(aliases))
	}
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return errors.New("metadata.aliases: empty alias not allowed")
		}
		if !packAliasPattern.MatchString(alias) {
			return fmt.Errorf("metadata.aliases: %q must match %s", alias, packAliasPattern.String())
		}
		if _, dup := seen[alias]; dup {
			return fmt.Errorf("metadata.aliases: duplicate entry %q", alias)
		}
		seen[alias] = struct{}{}
	}
	return nil
}

// packTopicPrefixes returns the set of "job.<x>." prefixes a pack may use
// for its topics. Always includes "job.<id>."; each declared alias adds
// "job.<alias>.". Callers iterate over the slice with HasPrefix; the slice
// is small (<=9 entries given maxPackAliases) so linear scan is fine.
func packTopicPrefixes(id string, aliases []string) []string {
	prefixes := make([]string, 0, 1+len(aliases))
	prefixes = append(prefixes, "job."+id+".")
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		prefixes = append(prefixes, "job."+alias+".")
	}
	return prefixes
}

// hasAnyPrefix reports whether s starts with any prefix in the slice.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// formatPrefixList formats a slice of `job.<x>.` prefixes for error
// messages. Single prefix → `job.<x>.*`; multiple → `job.<a>.* or
// job.<b>.* ...` so operators can see exactly which namespaces are
// allowed. Glob suffix is added inline since errors mention `.*` patterns.
func formatPrefixList(prefixes []string) string {
	parts := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		parts[i] = prefix + "*"
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, " or ")
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
	if err := validatePackAliases(manifest.Metadata.Aliases); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Metadata.Version) == "" {
		return errors.New("metadata.version required")
	}
	if minVersion := strings.TrimSpace(manifest.Compatibility.MinCoreVersion); minVersion != "" {
		if _, ok := parseSemver(minVersion); !ok {
			return fmt.Errorf("compatibility.minCoreVersion must be semver: %q", minVersion)
		}
	}
	if maxVersion := strings.TrimSpace(manifest.Compatibility.MaxCoreVersion); maxVersion != "" {
		if _, ok := parseSemver(maxVersion); !ok {
			return fmt.Errorf("compatibility.maxCoreVersion must be semver: %q", maxVersion)
		}
	}
	topicPrefixes := packTopicPrefixes(id, manifest.Metadata.Aliases)
	for _, topic := range manifest.Topics {
		if topic.Name == "" {
			return errors.New("topic name required")
		}
		if !hasAnyPrefix(topic.Name, topicPrefixes) {
			return fmt.Errorf("topic %q must be namespaced under %s", topic.Name, formatPrefixList(topicPrefixes))
		}
	}
	schemaIDs := make(map[string]struct{}, len(manifest.Resources.Schemas))
	for _, res := range manifest.Resources.Schemas {
		if res.ID == "" || res.Path == "" {
			return errors.New("schema id and path required")
		}
		if !strings.HasPrefix(res.ID, id+"/") {
			return fmt.Errorf("schema id %q must be namespaced under %s/", res.ID, id)
		}
		schemaIDs[res.ID] = struct{}{}
	}
	for _, topic := range manifest.Topics {
		for _, schemaID := range []string{
			strings.TrimSpace(topic.InputSchemaID),
			strings.TrimSpace(topic.OutputSchemaID),
		} {
			if schemaID == "" {
				continue
			}
			if _, ok := schemaIDs[schemaID]; !ok {
				return fmt.Errorf("topic %s references unknown schema %s", topic.Name, schemaID)
			}
		}
	}
	for _, res := range manifest.Resources.Workflows {
		if res.ID == "" || res.Path == "" {
			return errors.New("workflow id and path required")
		}
		if !strings.HasPrefix(res.ID, id+".") {
			return fmt.Errorf("workflow id %q must be namespaced under %s", res.ID, id)
		}
	}
	return nil
}

func ensureProtocolCompatible(manifest *packManifest) error {
	if manifest.Compatibility.ProtocolVersion == 0 {
		return errors.New("compatibility.protocolVersion required")
	}
	if manifest.Compatibility.ProtocolVersion != capsdk.DefaultProtocolVersion {
		return fmt.Errorf("pack protocol version %d is not compatible with this server (requires version %d); rebuild your pack with a compatible capsdk version", manifest.Compatibility.ProtocolVersion, capsdk.DefaultProtocolVersion)
	}
	return nil
}

func enforceCoreVersion(ctx context.Context, client *restClient, manifest *packManifest) error {
	if manifest == nil || client == nil {
		return nil
	}
	minVersion := strings.TrimSpace(manifest.Compatibility.MinCoreVersion)
	maxVersion := strings.TrimSpace(manifest.Compatibility.MaxCoreVersion)
	if minVersion == "" && maxVersion == "" {
		return nil
	}

	gatewayVersion, err := fetchGatewayVersion(ctx, client)
	if err != nil {
		fmt.Printf("warning: gateway version unavailable (%v); skipping core version check\n", err)
		return nil
	}
	if gatewayVersion == "" {
		fmt.Println("warning: gateway version unavailable; skipping core version check")
		return nil
	}
	parsedGateway, ok := parseSemver(gatewayVersion)
	if !ok {
		fmt.Printf("warning: gateway version %q not semver; skipping core version check\n", gatewayVersion)
		return nil
	}
	if minVersion != "" {
		parsedMin, _ := parseSemver(minVersion)
		if compareSemver(parsedGateway, parsedMin) < 0 {
			return fmt.Errorf("gateway version %s is below minCoreVersion %s", gatewayVersion, minVersion)
		}
	}
	if maxVersion != "" {
		parsedMax, _ := parseSemver(maxVersion)
		if compareSemver(parsedGateway, parsedMax) > 0 {
			return fmt.Errorf("gateway version %s is above maxCoreVersion %s", gatewayVersion, maxVersion)
		}
	}
	return nil
}

func fetchGatewayVersion(ctx context.Context, client *restClient) (string, error) {
	var resp struct {
		Build struct {
			Version string `json:"version"`
		} `json:"build"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/api/v1/status", nil, &resp); err != nil {
		return "", fmt.Errorf("fetch gateway version: %w", err)
	}
	return strings.TrimSpace(resp.Build.Version), nil
}

type semver struct {
	major int
	minor int
	patch int
	pre   string
}

func parseSemver(raw string) (semver, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return semver{}, false
	}
	value = strings.TrimPrefix(value, "v")
	main := value
	pre := ""
	if idx := strings.IndexAny(value, "-+"); idx >= 0 {
		main = value[:idx]
		if value[idx] == '-' {
			pre = value[idx+1:]
			if plus := strings.Index(pre, "+"); plus >= 0 {
				pre = pre[:plus]
			}
		}
	}
	parts := strings.Split(main, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return semver{}, false
	}
	major, ok := parseSemverInt(parts[0])
	if !ok {
		return semver{}, false
	}
	minor, ok := parseSemverInt(parts[1])
	if !ok {
		return semver{}, false
	}
	patch := 0
	if len(parts) == 3 {
		val, ok := parseSemverInt(parts[2])
		if !ok {
			return semver{}, false
		}
		patch = val
	}
	return semver{major: major, minor: minor, patch: patch, pre: pre}, true
}

func parseSemverInt(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return val, true
}

func compareSemver(left, right semver) int {
	if left.major != right.major {
		if left.major < right.major {
			return -1
		}
		return 1
	}
	if left.minor != right.minor {
		if left.minor < right.minor {
			return -1
		}
		return 1
	}
	if left.patch != right.patch {
		if left.patch < right.patch {
			return -1
		}
		return 1
	}
	if left.pre == right.pre {
		return 0
	}
	if left.pre == "" {
		return 1
	}
	if right.pre == "" {
		return -1
	}
	if left.pre < right.pre {
		return -1
	}
	if left.pre > right.pre {
		return 1
	}
	return 0
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
		return fmt.Errorf("run policy simulation %s: %w", test.Name, err)
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

func listPackRecords(ctx context.Context, client *restClient) (map[string]packRecord, error) {
	records, _, err := loadPackRegistry(ctx, client)
	return records, err
}

func getPackRecord(ctx context.Context, client *restClient, packID string) (*packRecord, error) {
	records, _, err := loadPackRegistry(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("get pack record %s: %w", packID, err)
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
		return nil, nil, fmt.Errorf("load pack registry: %w", err)
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	records := map[string]packRecord{}
	raw := normalizeJSON(doc.Data["installed"])
	if raw == nil {
		return records, doc, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("load pack registry: %w", err)
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, nil, fmt.Errorf("load pack registry: %w", err)
	}
	return records, doc, nil
}

func loadSchemaFile(dir, relPath string) (map[string]any, string, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, "", fmt.Errorf("load schema file %s: %w", relPath, err)
	}
	payload, err := loadDataFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("load schema file %s: %w", relPath, err)
	}
	schemaMap, ok := payload.(map[string]any)
	if !ok {
		return nil, "", errors.New("schema file must be an object")
	}
	digest, err := hashValue(schemaMap)
	if err != nil {
		return nil, "", fmt.Errorf("load schema file %s: %w", relPath, err)
	}
	return schemaMap, digest, nil
}

func loadWorkflowFile(dir, relPath, id string) (map[string]any, string, error) {
	path, err := safeJoin(dir, relPath)
	if err != nil {
		return nil, "", fmt.Errorf("load workflow file %s: %w", relPath, err)
	}
	payload, err := loadDataFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("load workflow file %s: %w", relPath, err)
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
		return nil, "", fmt.Errorf("load workflow file %s: %w", relPath, err)
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
		return nil, fmt.Errorf("load data file: %w", err)
	}
	var payload any
	if json.Unmarshal(data, &payload) == nil {
		return normalizeJSON(payload), nil
	}
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("load data file: %w", err)
	}
	return normalizeJSON(payload), nil
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
			return fmt.Errorf("canonical encode: %w", err)
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
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return fmt.Errorf("canonical map key: %w", err)
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		if err := appendCanonical(buf, m[k]); err != nil {
			return fmt.Errorf("canonical encode: %w", err)
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
			return fmt.Errorf("canonical encode: %w", err)
		}
	}
	buf.WriteByte(']')
	return nil
}

type restClient struct {
	baseURL    string
	apiKey     string
	tenantID   string
	httpClient *http.Client
}

func newRestClient(gateway, apiKey, tenantID string, transport *http.Transport) *restClient {
	c := &http.Client{Timeout: 20 * time.Second}
	if transport != nil {
		c.Transport = transport
	}
	return &restClient{
		baseURL:    strings.TrimRight(gateway, "/"),
		apiKey:     apiKey,
		tenantID:   strings.TrimSpace(tenantID),
		httpClient: c,
	}
}

func (c *restClient) endpoint(path string) string {
	return c.baseURL + path
}

func (c *restClient) installPack(ctx context.Context, archive []byte, force, upgrade, inactive bool) (*packInstallResult, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("bundle", "bundle.tgz")
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(archive); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		name  string
		value bool
	}{{"force", force}, {"upgrade", upgrade}, {"inactive", inactive}} {
		if err := mw.WriteField(field.name, strconv.FormatBool(field.value)); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	var result packInstallResult
	if err := c.do(ctx, http.MethodPost, "/api/v1/packs/install", mw.FormDataContentType(), &body, &result, 2*time.Minute); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *restClient) uninstallPack(ctx context.Context, packID string, purge bool) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/packs/"+url.PathEscape(packID)+"/uninstall", map[string]bool{"purge": purge}, nil)
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
			return fmt.Errorf("rest %s %s: %w", method, path, err)
		}
		payload = strings.NewReader(buf.String())
	}
	return c.do(ctx, method, path, "application/json", payload, out, 20*time.Second)
}

func (c *restClient) do(ctx context.Context, method, path, contentType string, body io.Reader, out any, timeout time.Duration) error {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), body)
	if err != nil {
		return fmt.Errorf("rest %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if c.tenantID != "" {
		req.Header.Set("X-Tenant-ID", c.tenantID)
	}
	client := *c.httpClient
	client.Timeout = timeout
	resp, err := client.Do(req) // #nosec -- base URL is operator-provided via CLI.
	if err != nil {
		return fmt.Errorf("rest %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Limit error body read to 1 MiB to prevent OOM from oversized responses.
		const maxErrBody = 1 << 20
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		msg := strings.TrimSpace(string(data))
		if msg == "" || readErr != nil {
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
	path := "/api/v1/config?scope=" + url.QueryEscape(scope) + "&scope_id=" + url.QueryEscape(scopeID) + "&envelope=true"
	var doc configDoc
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (c *restClient) getSchema(ctx context.Context, id string) (map[string]any, error) {
	var resp struct {
		Schema map[string]any `json:"schema"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/schemas/"+url.PathEscape(id), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Schema, nil
}

func (c *restClient) getWorkflow(ctx context.Context, id string) (map[string]any, error) {
	var resp map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workflows/"+url.PathEscape(id), nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func isURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func downloadToTemp(raw string, transport *http.Transport) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("download to temp: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("url host required")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("download to temp: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if transport != nil {
		client.Transport = transport
	}
	resp, err := client.Do(req) // #nosec -- URL is operator-provided and validated.
	if err != nil {
		return "", fmt.Errorf("download to temp: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	tmpFile, err := os.CreateTemp("", "cordum-pack-*.tgz")
	if err != nil {
		return "", fmt.Errorf("download to temp: %w", err)
	}
	defer func() { _ = tmpFile.Close() }()
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return "", fmt.Errorf("download to temp: %w", err)
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
		return fmt.Errorf("extract tar.gz: %w", err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("extract tar.gz: %w", err)
	}
	defer func() { _ = gz.Close() }()
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
			return fmt.Errorf("extract tar.gz: %w", err)
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return fmt.Errorf("extract tar.gz: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil { // #nosec -- target path is validated by safeJoin.
				return fmt.Errorf("extract tar.gz: %w", err)
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
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil { // #nosec -- target path is validated by safeJoin.
				return fmt.Errorf("extract tar.gz: %w", err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec -- target path is validated by safeJoin.
			if err != nil {
				return fmt.Errorf("extract tar.gz: %w", err)
			}
			if _, err := io.CopyN(out, tr, hdr.Size); err != nil && !errors.Is(err, io.EOF) {
				if cerr := out.Close(); cerr != nil {
					return cerr
				}
				return fmt.Errorf("extract tar.gz: %w", err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("extract tar.gz: %w", err)
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
