package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	PermDelegationImpersonate  = "delegation.impersonate"
	PermWorkflowsRead          = "workflows.read"
	PermWorkflowsWrite         = "workflows.write"
	PermWorkersRead            = "workers.read"
	PermWorkersWrite           = "workers.write"
	PermConfigRead             = "config.read"
	PermConfigWrite            = "config.write"
	PermAuditRead              = "audit.read"
	PermAuditExport            = "audit.export"
	PermAuditVerify            = "audit.verify"
	PermAPIKeysRead            = "apiKeys.read"
	PermAPIKeysWrite           = "apiKeys.write"
	PermDLQRead                = "dlq.read"
	PermDLQWrite               = "dlq.write"
	PermMemoryRead             = "memory.read"
	PermLegalHoldRead          = "legalHold.read"
	PermLegalHoldWrite         = "legalHold.write"
	PermLicenseRead            = "license.read"
	PermLocksRead              = "locks.read"
	PermMCPRead                = "mcp.read"
	PermMCPVerify              = "mcp.verify"
	PermPacksInstall           = "packs.install"
	PermPacksUninstall         = "packs.uninstall"
	PermPacksRead              = "packs.read"
	PermPacksVerify            = "packs.verify"
	PermPolicyRead             = "policy.read"
	PermPolicyWrite            = "policy.write"
	PermPoolsWrite             = "pools.write"
	PermTelemetryRead          = "telemetry.read"
	PermTelemetryWrite         = "telemetry.write"
	PermTelemetryExport        = "telemetry.export"
	PermTopicsRead             = "topics.read"
	PermTopicsWrite            = "topics.write"
	PermWorkerCredentialsRead  = "workerCredentials.read"  // #nosec G101 -- permission identifier, not a credential.
	PermWorkerCredentialsWrite = "workerCredentials.write" // #nosec G101 -- permission identifier, not a credential.
	PermGovernanceRead         = "governance.read"
	PermSchemasRead            = "schemas.read"
	PermSchemasWrite           = "schemas.write"
	PermUsersRead              = "users.read"
	PermUsersWrite             = "users.write"
	PermRolesRead              = "roles.read"
	PermRolesWrite             = "roles.write"

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
	PermTelemetryRead, PermTelemetryWrite, PermTelemetryExport,
	PermTopicsRead, PermTopicsWrite,
	PermWorkerCredentialsRead, PermWorkerCredentialsWrite,
	PermGovernanceRead,
	PermSchemasRead, PermSchemasWrite,
	PermUsersRead, PermUsersWrite,
	PermRolesRead, PermRolesWrite,
	PermEvalsDatasetsRead, PermEvalsDatasetsWrite, PermEvalsDatasetsDelete,
	PermEvalsRunsExecute, PermEvalsRunsRead, PermEvalsRunsDelete,
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

// DefaultRoles returns the three built-in roles.
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
	}
}

// ---------------------------------------------------------------------------
// Basic role fallback mapping (used when RBAC entitlement is disabled)
// ---------------------------------------------------------------------------

// basicRolePermissions maps the three built-in role names to their permissions.
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
	name = strings.ToLower(strings.TrimSpace(name))
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
	role.Name = strings.ToLower(strings.TrimSpace(role.Name))
	if role.Name == "" {
		return fmt.Errorf("role name required")
	}
	if role.CreatedAt == "" {
		role.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	role.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.Marshal(role)
	if err != nil {
		return fmt.Errorf("marshal role: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, rbacRoleKeyPrefix+role.Name, data, 0)
	pipe.SAdd(ctx, rbacRoleSetKey, role.Name)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis put role: %w", err)
	}
	return nil
}

// DeleteRole removes a role definition. Built-in roles cannot be deleted.
func (s *RBACStore) DeleteRole(ctx context.Context, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("role name required")
	}
	// Check if role exists and is not built-in
	existing, err := s.GetRole(ctx, name)
	if err != nil {
		return err
	}
	if existing.BuiltIn {
		return ErrBuiltInRole
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, rbacRoleKeyPrefix+name)
	pipe.SRem(ctx, rbacRoleSetKey, name)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis delete role: %w", err)
	}
	return nil
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
	visited := make(map[string]bool)
	return s.resolvePermissionsRecursive(ctx, roleName, visited)
}

func (s *RBACStore) resolvePermissionsRecursive(ctx context.Context, roleName string, visited map[string]bool) ([]string, error) {
	roleName = strings.ToLower(strings.TrimSpace(roleName))
	if roleName == "" {
		return nil, nil
	}
	if visited[roleName] {
		return nil, fmt.Errorf("circular inheritance detected: role %q", roleName)
	}
	visited[roleName] = true

	role, err := s.GetRole(ctx, roleName)
	if err != nil {
		return nil, err
	}

	permSet := make(map[string]bool, len(role.Permissions))
	for _, p := range role.Permissions {
		permSet[p] = true
	}

	for _, parent := range role.Inherits {
		inherited, err := s.resolvePermissionsRecursive(ctx, parent, visited)
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
	return perms, nil
}

// ValidateInheritance checks that a role's inheritance chain has no cycles
// and all referenced parent roles exist.
func (s *RBACStore) ValidateInheritance(ctx context.Context, roleName string, inherits []string) error {
	// Build a temporary view: pretend the role has the given parents
	visited := map[string]bool{strings.ToLower(strings.TrimSpace(roleName)): true}
	for _, parent := range inherits {
		if err := s.checkInheritanceChain(ctx, parent, visited); err != nil {
			return err
		}
	}
	return nil
}

func (s *RBACStore) checkInheritanceChain(ctx context.Context, roleName string, visited map[string]bool) error {
	roleName = strings.ToLower(strings.TrimSpace(roleName))
	if roleName == "" {
		return nil
	}
	if visited[roleName] {
		return fmt.Errorf("circular inheritance: role %q would create a cycle", roleName)
	}
	visited[roleName] = true

	role, err := s.GetRole(ctx, roleName)
	if err != nil {
		return fmt.Errorf("unknown parent role %q: %w", roleName, err)
	}
	for _, parent := range role.Inherits {
		if err := s.checkInheritanceChain(ctx, parent, visited); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// HasPermission checks
// ---------------------------------------------------------------------------

// HasPermission checks if a role has a specific permission, resolving through
// the inheritance hierarchy. When the RBAC entitlement is disabled, it uses
// the basic role mapping.
func HasPermission(ctx context.Context, store *RBACStore, roleName, permission string, rbacEntitled bool) bool {
	roleName = strings.ToLower(strings.TrimSpace(roleName))
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
