package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// Permission constants
// ---------------------------------------------------------------------------

const (
	PermAdminAll       = "admin.*"
	PermJobsRead       = "jobs.read"
	PermJobsWrite      = "jobs.write"
	PermJobsApprove    = "jobs.approve"
	PermAgentsRead     = "agents.read"
	PermAgentsWrite    = "agents.write"
	PermAgentsDelegate = "agents.delegate"
	PermDelegationRead = "delegation.read"
	// PermDelegationImpersonate authorises a caller to submit a job
	// asserting a delegation_audience_agent_id that differs from the
	// caller's authenticated agent_id. Without this permission, any
	// audience widening on submit is rejected 403 to prevent quiet
	// impersonation through the delegation wire path. See #198
	// Blocker 1 follow-up on split/delegation-security.
	PermDelegationImpersonate = "delegation.impersonate"
	// PermShadowExceptionHighRisk authorises an operator to create or
	// revoke an exception scoped to risk=high findings (EDGE-143.6, Q8
	// binding governor ruling on task-de50a293). Acts as the step-up
	// analogue to PermDelegationImpersonate: holders are presumed to
	// have completed the recent-MFA workflow before the grant. Without
	// this permission OR the admin legacy role, risk=high exception
	// CREATE/REVOKE returns 403 with code `step_up_required`. Findings
	// matching the exception scope are silenced from default operator
	// queries, so this gate is materially privilege-equivalent to
	// modifying the audit posture for an entire scope of findings.
	PermShadowExceptionHighRisk = "shadow.exception.high_risk"
	PermWorkflowsRead           = "workflows.read"
	PermWorkflowsWrite          = "workflows.write"
	PermWorkersRead             = "workers.read"
	PermWorkersWrite            = "workers.write"
	PermConfigRead              = "config.read"
	PermConfigWrite             = "config.write"
	PermAuditRead               = "audit.read"
	PermAuditExport             = "audit.export"
	PermAuditVerify             = "audit.verify"
	PermAPIKeysRead             = "apiKeys.read"
	PermAPIKeysWrite            = "apiKeys.write"
	PermDLQRead                 = "dlq.read"
	PermDLQWrite                = "dlq.write"
	PermMemoryRead              = "memory.read"
	PermLegalHoldRead           = "legalHold.read"
	PermLegalHoldWrite          = "legalHold.write"
	PermLicenseRead             = "license.read"
	PermLocksRead               = "locks.read"
	PermMCPRead                 = "mcp.read"
	PermMCPVerify               = "mcp.verify"
	PermPacksInstall            = "packs.install"
	PermPacksUninstall          = "packs.uninstall"
	PermPacksRead               = "packs.read"
	PermPacksVerify             = "packs.verify"
	PermPolicyRead              = "policy.read"
	PermPolicyWrite             = "policy.write"
	PermPoolsWrite              = "pools.write"
	PermRuntimeIngest           = "edge.runtime.ingest"
	PermTelemetryRead           = "telemetry.read"
	PermTelemetryWrite          = "telemetry.write"
	PermTelemetryExport         = "telemetry.export"
	PermTopicsRead              = "topics.read"
	PermTopicsWrite             = "topics.write"
	PermWorkerCredentialsRead   = "workerCredentials.read"  // #nosec G101 -- permission identifier, not a credential.
	PermWorkerCredentialsWrite  = "workerCredentials.write" // #nosec G101 -- permission identifier, not a credential.
	PermGovernanceRead          = "governance.read"
	PermSchemasRead             = "schemas.read"
	PermSchemasWrite            = "schemas.write"
	PermUsersRead               = "users.read"
	PermUsersWrite              = "users.write"
	PermRolesRead               = "roles.read"
	PermRolesWrite              = "roles.write"

	// Eval dataset permissions gate the phase-2 governance-regression
	// pipeline. They are namespaced under `evals.datasets.*` so the
	// broader `evals.*` area stays available for sibling work (runner,
	// replay comparisons) without churning this constant block.
	PermEvalsDatasetsRead   = "evals.datasets.read"
	PermEvalsDatasetsWrite  = "evals.datasets.write"
	PermEvalsDatasetsDelete = "evals.datasets.delete"
	PermEvalsRunsExecute    = "evals.runs.execute"
	PermEvalsRunsRead       = "evals.runs.read"
	PermEvalsRunsDelete     = "evals.runs.delete"
)

// AllPermissions is the canonical list of permissions for validation.
var AllPermissions = []string{
	PermAdminAll,
	PermJobsRead, PermJobsWrite, PermJobsApprove,
	PermAgentsRead, PermAgentsWrite, PermAgentsDelegate, PermDelegationRead,
	PermWorkflowsRead, PermWorkflowsWrite,
	PermWorkersRead, PermWorkersWrite,
	PermConfigRead, PermConfigWrite,
	PermAuditRead, PermAuditExport, PermAuditVerify,
	PermAPIKeysRead, PermAPIKeysWrite,
	PermDLQRead, PermDLQWrite,
	PermMemoryRead,
	PermLegalHoldRead, PermLegalHoldWrite,
	PermLicenseRead,
	PermLocksRead,
	PermMCPRead, PermMCPVerify,
	PermPacksInstall, PermPacksUninstall, PermPacksRead, PermPacksVerify,
	PermPolicyRead, PermPolicyWrite,
	PermPoolsWrite,
	PermRuntimeIngest,
	PermTelemetryRead, PermTelemetryWrite, PermTelemetryExport,
	PermTopicsRead, PermTopicsWrite,
	PermWorkerCredentialsRead, PermWorkerCredentialsWrite,
	PermGovernanceRead,
	PermSchemasRead, PermSchemasWrite,
	PermUsersRead, PermUsersWrite,
	PermRolesRead, PermRolesWrite,
	PermEvalsDatasetsRead, PermEvalsDatasetsWrite, PermEvalsDatasetsDelete,
	PermEvalsRunsExecute, PermEvalsRunsRead, PermEvalsRunsDelete,
	PermDelegationImpersonate, PermShadowExceptionHighRisk,
}

var roleNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)

// NormalizeRoleName returns the canonical stored form for a role name.
func NormalizeRoleName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ValidateRoleName enforces the public role-name contract.
func ValidateRoleName(name string) error {
	if !roleNamePattern.MatchString(name) {
		return fmt.Errorf("role name must match ^[a-z0-9][a-z0-9_-]{1,62}$: %q", name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// RoleDefinition
// ---------------------------------------------------------------------------

// RoleDefinition describes a named role with explicit permissions and inheritance.
type RoleDefinition struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Permissions []string `json:"permissions"`
	Inherits    []string `json:"inherits,omitempty"`
	BuiltIn     bool     `json:"built_in"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

// DefaultRoles returns the built-in interactive roles.
func DefaultRoles() []*RoleDefinition {
	now := time.Now().UTC().Format(time.RFC3339)
	return []*RoleDefinition{
		{
			Name:        "admin",
			Description: "Full access to all resources",
			Permissions: []string{PermAdminAll},
			BuiltIn:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			Name:        "viewer",
			Description: "Read-only access to all resources",
			Permissions: []string{
				PermJobsRead,
				PermWorkflowsRead,
				PermWorkersRead,
				PermLocksRead,
				PermTopicsRead,
				PermConfigRead,
				PermAuditRead,
				PermSchemasRead,
				PermPolicyRead,
				PermGovernanceRead,
				PermDelegationRead,
			},
			BuiltIn:   true,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			Name:        "operator",
			Description: "Manage jobs, workflows, approvals, packs, and schemas",
			Permissions: []string{
				PermJobsRead, PermJobsWrite, PermJobsApprove,
				PermWorkflowsRead, PermWorkflowsWrite,
				PermWorkersRead,
				PermLocksRead,
				PermTopicsRead,
				PermPacksInstall, PermPacksUninstall,
				PermSchemasRead, PermSchemasWrite,
				PermPolicyRead,
				PermGovernanceRead,
				PermDelegationRead,
				PermAuditRead,
				PermConfigRead,
			},
			Inherits:  []string{"viewer"},
			BuiltIn:   true,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
}

// ---------------------------------------------------------------------------
// Basic role fallback mapping (used when RBAC entitlement is disabled)
// ---------------------------------------------------------------------------

// basicRolePermissions maps built-in/fallback role names to their permissions.
// Used as fallback when the RBAC entitlement is not active.
var basicRolePermissions = map[string][]string{
	"admin": {PermAdminAll},
	"operator": {
		PermJobsRead, PermJobsWrite, PermJobsApprove,
		PermWorkflowsRead, PermWorkflowsWrite,
		PermWorkersRead,
		PermLocksRead,
		PermTopicsRead,
		PermPacksInstall, PermPacksUninstall,
		PermSchemasRead, PermSchemasWrite,
		PermPolicyRead,
		PermGovernanceRead,
		PermDelegationRead,
		PermAuditRead,
		PermConfigRead,
	},
	"viewer": {
		PermJobsRead,
		PermWorkflowsRead,
		PermWorkersRead,
		PermLocksRead,
		PermTopicsRead,
		PermConfigRead,
		PermAuditRead,
		PermSchemasRead,
		PermPolicyRead,
		PermGovernanceRead,
		PermDelegationRead,
	},
	"runtime_collector": {
		PermRuntimeIngest,
	},
}

// ---------------------------------------------------------------------------
// RBACStore — Redis-backed role storage
// ---------------------------------------------------------------------------

const (
	rbacRoleKeyPrefix = "rbac:role:"
	rbacRoleSetKey    = "rbac:roles"
)

// RBACStore manages RBAC role definitions in Redis.
type RBACStore struct {
	client *redis.Client
}

// NewRBACStore creates a new Redis-backed RBAC store.
func NewRBACStore(redisURL string) (*RBACStore, error) {
	opts, err := redisutil.ParseOptions(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RBACStore{client: client}, nil
}

// NewRBACStoreFromClient creates an RBACStore using an existing Redis client.
func NewRBACStoreFromClient(client *redis.Client) *RBACStore {
	return &RBACStore{client: client}
}

// BootstrapDefaultRoles seeds the default roles if they don't already exist.
func (s *RBACStore) BootstrapDefaultRoles(ctx context.Context) error {
	for _, role := range DefaultRoles() {
		exists, err := s.client.Exists(ctx, rbacRoleKeyPrefix+role.Name).Result()
		if err != nil {
			return fmt.Errorf("check role exists %s: %w", role.Name, err)
		}
		if exists > 0 {
			continue
		}
		if err := s.PutRole(ctx, role); err != nil {
			return fmt.Errorf("bootstrap role %s: %w", role.Name, err)
		}
	}
	return nil
}

// GetRole retrieves a role definition by name.
func (s *RBACStore) GetRole(ctx context.Context, name string) (*RoleDefinition, error) {
	name = NormalizeRoleName(name)
	if name == "" {
		return nil, fmt.Errorf("role name required")
	}
	data, err := s.client.Get(ctx, rbacRoleKeyPrefix+name).Bytes()
	if err == redis.Nil {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get role: %w", err)
	}
	var role RoleDefinition
	if err := json.Unmarshal(data, &role); err != nil {
		return nil, fmt.Errorf("unmarshal role: %w", err)
	}
	return &role, nil
}

// ListRoles returns all role definitions.
func (s *RBACStore) ListRoles(ctx context.Context) ([]*RoleDefinition, error) {
	names, err := s.client.SMembers(ctx, rbacRoleSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis smembers roles: %w", err)
	}
	if len(names) == 0 {
		return []*RoleDefinition{}, nil
	}
	sort.Strings(names)
	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(names))
	for i, name := range names {
		cmds[i] = pipe.Get(ctx, rbacRoleKeyPrefix+name)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("redis pipeline roles: %w", err)
	}

	roles := make([]*RoleDefinition, 0, len(names))
	for _, cmd := range cmds {
		data, err := cmd.Bytes()
		if err != nil {
			continue
		}
		var role RoleDefinition
		if err := json.Unmarshal(data, &role); err != nil {
			continue
		}
		roles = append(roles, &role)
	}
	return roles, nil
}

// PutRole creates or updates a role definition.
func (s *RBACStore) PutRole(ctx context.Context, role *RoleDefinition) error {
	if role == nil {
		return fmt.Errorf("role required")
	}
	role.Name = NormalizeRoleName(role.Name)
	if role.Name == "" {
		return fmt.Errorf("role name required")
	}
	if err := ValidateRoleName(role.Name); err != nil {
		return err
	}
	if role.CreatedAt == "" {
		role.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	role.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	keys, err := s.roleWatchKeys(ctx)
	if err != nil {
		return err
	}
	return s.client.Watch(ctx, func(tx *redis.Tx) error {
		roles, err := loadRBACRoleSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		if existing, ok := roles[role.Name]; ok && role.CreatedAt == "" {
			role.CreatedAt = existing.CreatedAt
		}
		roles[role.Name] = role
		if err := validateInheritanceSnapshot(role.Name, role.Inherits, roles); err != nil {
			return err
		}
		data, err := json.Marshal(role)
		if err != nil {
			return fmt.Errorf("marshal role: %w", err)
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, rbacRoleKeyPrefix+role.Name, data, 0)
			pipe.SAdd(ctx, rbacRoleSetKey, role.Name)
			return nil
		})
		return err
	}, keys...)
}

// DeleteRole removes a role definition. Built-in roles cannot be deleted.
func (s *RBACStore) DeleteRole(ctx context.Context, name string) error {
	name = NormalizeRoleName(name)
	if name == "" {
		return fmt.Errorf("role name required")
	}
	if err := ValidateRoleName(name); err != nil {
		return err
	}
	keys, err := s.roleWatchKeys(ctx)
	if err != nil {
		return err
	}
	return s.client.Watch(ctx, func(tx *redis.Tx) error {
		roles, err := loadRBACRoleSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		existing, ok := roles[name]
		if !ok {
			return ErrRoleNotFound
		}
		if existing.BuiltIn {
			return ErrBuiltInRole
		}
		if refs := referencingRoles(name, roles); len(refs) > 0 {
			return &RoleInUseError{Role: name, Referencing: refs}
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Del(ctx, rbacRoleKeyPrefix+name)
			pipe.SRem(ctx, rbacRoleSetKey, name)
			return nil
		})
		return err
	}, keys...)
}

func (s *RBACStore) roleWatchKeys(ctx context.Context) ([]string, error) {
	names, err := s.client.SMembers(ctx, rbacRoleSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis role keys: %w", err)
	}
	keys := make([]string, 0, len(names)+1)
	keys = append(keys, rbacRoleSetKey)
	for _, name := range names {
		keys = append(keys, rbacRoleKeyPrefix+NormalizeRoleName(name))
	}
	return keys, nil
}

type roleSnapshotReader interface {
	SMembers(context.Context, string) *redis.StringSliceCmd
	Get(context.Context, string) *redis.StringCmd
}

func loadRBACRoleSnapshot(ctx context.Context, reader roleSnapshotReader) (map[string]*RoleDefinition, error) {
	names, err := reader.SMembers(ctx, rbacRoleSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis smembers roles: %w", err)
	}
	roles := make(map[string]*RoleDefinition, len(names))
	for _, name := range names {
		role, err := loadRBACRole(ctx, reader, NormalizeRoleName(name))
		if errors.Is(err, ErrRoleNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		roles[role.Name] = role
	}
	return roles, nil
}

func loadRBACRole(ctx context.Context, reader roleSnapshotReader, name string) (*RoleDefinition, error) {
	data, err := reader.Get(ctx, rbacRoleKeyPrefix+name).Bytes()
	if err == redis.Nil {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get role: %w", err)
	}
	var role RoleDefinition
	if err := json.Unmarshal(data, &role); err != nil {
		return nil, fmt.Errorf("unmarshal role: %w", err)
	}
	role.Name = NormalizeRoleName(role.Name)
	return &role, nil
}

func validateInheritanceSnapshot(roleName string, inherits []string, roles map[string]*RoleDefinition) error {
	inProgress := map[string]struct{}{NormalizeRoleName(roleName): {}}
	completed := make(map[string]struct{})
	for _, parent := range inherits {
		if err := checkInheritanceSnapshot(parent, roles, inProgress, completed); err != nil {
			return err
		}
	}
	return nil
}

func checkInheritanceSnapshot(roleName string, roles map[string]*RoleDefinition, inProgress, completed map[string]struct{}) error {
	roleName = NormalizeRoleName(roleName)
	if roleName == "" {
		return nil
	}
	if _, ok := completed[roleName]; ok {
		return nil
	}
	if _, ok := inProgress[roleName]; ok {
		return fmt.Errorf("circular inheritance: role %q would create a cycle", roleName)
	}
	role, ok := roles[roleName]
	if !ok {
		return fmt.Errorf("unknown parent role %q: %w", roleName, ErrRoleNotFound)
	}
	inProgress[roleName] = struct{}{}
	defer delete(inProgress, roleName)
	for _, parent := range role.Inherits {
		if err := checkInheritanceSnapshot(parent, roles, inProgress, completed); err != nil {
			return err
		}
	}
	completed[roleName] = struct{}{}
	return nil
}

func referencingRoles(name string, roles map[string]*RoleDefinition) []string {
	name = NormalizeRoleName(name)
	refs := make([]string, 0)
	for roleName, role := range roles {
		for _, parent := range role.Inherits {
			if NormalizeRoleName(parent) == name {
				refs = append(refs, roleName)
				break
			}
		}
	}
	sort.Strings(refs)
	return refs
}

// Close closes the Redis client connection.
func (s *RBACStore) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Permission resolution
// ---------------------------------------------------------------------------

// ResolvePermissions returns the full set of permissions for a role,
// flattening the inheritance hierarchy. Returns an error if circular
// inheritance is detected.
func (s *RBACStore) ResolvePermissions(ctx context.Context, roleName string) ([]string, error) {
	inProgress := make(map[string]struct{})
	completed := make(map[string][]string)
	return s.resolvePermissionsRecursive(ctx, roleName, inProgress, completed)
}

func (s *RBACStore) resolvePermissionsRecursive(ctx context.Context, roleName string, inProgress map[string]struct{}, completed map[string][]string) ([]string, error) {
	roleName = NormalizeRoleName(roleName)
	if roleName == "" {
		return nil, nil
	}
	if perms, ok := completed[roleName]; ok {
		return append([]string(nil), perms...), nil
	}
	if _, ok := inProgress[roleName]; ok {
		return nil, fmt.Errorf("circular inheritance detected: role %q", roleName)
	}
	inProgress[roleName] = struct{}{}
	defer delete(inProgress, roleName)

	role, err := s.GetRole(ctx, roleName)
	if err != nil {
		return nil, err
	}

	permSet := make(map[string]bool, len(role.Permissions))
	for _, p := range role.Permissions {
		permSet[p] = true
	}

	for _, parent := range role.Inherits {
		inherited, err := s.resolvePermissionsRecursive(ctx, parent, inProgress, completed)
		if err != nil {
			return nil, err
		}
		for _, p := range inherited {
			permSet[p] = true
		}
	}

	perms := make([]string, 0, len(permSet))
	for p := range permSet {
		perms = append(perms, p)
	}
	sort.Strings(perms)
	completed[roleName] = append([]string(nil), perms...)
	return append([]string(nil), perms...), nil
}

// ValidateInheritance checks that a role's inheritance chain has no cycles
// and all referenced parent roles exist.
func (s *RBACStore) ValidateInheritance(ctx context.Context, roleName string, inherits []string) error {
	roleName = NormalizeRoleName(roleName)
	inProgress := map[string]struct{}{roleName: {}}
	completed := make(map[string]struct{})
	for _, parent := range inherits {
		if err := s.checkInheritanceChain(ctx, parent, inProgress, completed); err != nil {
			return err
		}
	}
	return nil
}

func (s *RBACStore) checkInheritanceChain(ctx context.Context, roleName string, inProgress, completed map[string]struct{}) error {
	roleName = NormalizeRoleName(roleName)
	if roleName == "" {
		return nil
	}
	if _, ok := completed[roleName]; ok {
		return nil
	}
	if _, ok := inProgress[roleName]; ok {
		return fmt.Errorf("circular inheritance: role %q would create a cycle", roleName)
	}
	inProgress[roleName] = struct{}{}
	defer delete(inProgress, roleName)

	role, err := s.GetRole(ctx, roleName)
	if err != nil {
		return fmt.Errorf("unknown parent role %q: %w", roleName, err)
	}
	for _, parent := range role.Inherits {
		if err := s.checkInheritanceChain(ctx, parent, inProgress, completed); err != nil {
			return err
		}
	}
	completed[roleName] = struct{}{}
	return nil
}

// ---------------------------------------------------------------------------
// HasPermission checks
// ---------------------------------------------------------------------------

// HasPermission checks if a role has a specific permission, resolving through
// the inheritance hierarchy. When the RBAC entitlement is disabled, it uses
// the basic role mapping.
func HasPermission(ctx context.Context, store *RBACStore, roleName, permission string, rbacEntitled bool) bool {
	roleName = NormalizeRoleName(roleName)
	permission = strings.TrimSpace(permission)
	if roleName == "" || permission == "" {
		return false
	}

	if !rbacEntitled || store == nil {
		return hasPermissionBasic(roleName, permission)
	}

	perms, err := store.ResolvePermissions(ctx, roleName)
	if err != nil {
		slog.Warn("rbac: failed to resolve permissions, falling back to basic",
			"role", roleName, "error", err)
		return hasPermissionBasic(roleName, permission)
	}
	return matchPermission(perms, permission)
}

// hasPermissionBasic checks permission using the hardcoded basic role mapping.
func hasPermissionBasic(roleName, permission string) bool {
	perms, ok := basicRolePermissions[roleName]
	if !ok {
		return false
	}
	return matchPermission(perms, permission)
}

// matchPermission checks if a permission matches any entry in the permission set.
// Supports wildcard admin.* which grants all permissions.
func matchPermission(perms []string, required string) bool {
	for _, p := range perms {
		if p == required {
			return true
		}
		if p == PermAdminAll {
			return true
		}
		// Support namespace wildcards: e.g. "jobs.*" matches "jobs.read"
		if prefix, ok := strings.CutSuffix(p, ".*"); ok {
			if strings.HasPrefix(required, prefix+".") {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Entitlement check helper
// ---------------------------------------------------------------------------

// RBACEntitled returns true if the RBAC entitlement is active.
func RBACEntitled(entitlements licensing.Entitlements) bool {
	return entitlements.RBAC
}

// ---------------------------------------------------------------------------
// RBAC errors
// ---------------------------------------------------------------------------

var (
	ErrRoleNotFound = fmt.Errorf("role not found")
	ErrBuiltInRole  = fmt.Errorf("cannot delete built-in role")
)

// RoleInUseError reports that a role cannot be deleted because other roles
// inherit it. Referencing is sorted and safe to return in API responses.
type RoleInUseError struct {
	Role        string
	Referencing []string
}

func (e *RoleInUseError) Error() string {
	if e == nil {
		return "role in use"
	}
	return fmt.Sprintf("role %q is inherited by: %s", e.Role, strings.Join(e.Referencing, ", "))
}
