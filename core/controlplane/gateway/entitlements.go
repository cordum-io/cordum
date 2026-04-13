package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

func resolveEntitlementResolver(resolvers ...*licensing.EntitlementResolver) *licensing.EntitlementResolver {
	for _, resolver := range resolvers {
		if resolver != nil {
			return resolver
		}
	}
	resolver := licensing.NewEntitlementResolver()
	resolver.Init()
	return resolver
}

func (s *server) entitlementResolver() *licensing.EntitlementResolver {
	if s != nil && s.entitlements != nil {
		return s.entitlements
	}
	return nil
}

func (s *server) resolvedPlan() licensing.Plan {
	if resolver := s.entitlementResolver(); resolver != nil {
		return resolver.ResolvedPlan()
	}
	return licensing.PlanCommunity
}

func (s *server) currentEntitlements() licensing.Entitlements {
	if resolver := s.entitlementResolver(); resolver != nil {
		return resolver.Entitlements()
	}
	return licensing.DefaultEntitlements(licensing.PlanCommunity)
}

func (s *server) currentLicenseInfo() *licensing.LicenseInfo {
	if resolver := s.entitlementResolver(); resolver != nil {
		return resolver.LicenseInfo()
	}
	info := licensing.NewEntitlementResolver().LicenseInfo()
	if info == nil {
		return nil
	}
	return info
}

func (s *server) currentLicenseRights() *licensing.Rights {
	if resolver := s.entitlementResolver(); resolver != nil {
		return resolver.Rights()
	}
	return nil
}

func (s *server) approvalModeLimit() string {
	mode := strings.TrimSpace(s.currentEntitlements().ApprovalMode)
	if mode == "" {
		return string(licensing.ApprovalModeSingle)
	}
	return mode
}

func (s *server) promptCharLimit() int {
	if limit := s.currentEntitlements().MaxPromptChars; limit > 0 {
		if limit > int64(^uint(0)>>1) {
			return int(^uint(0) >> 1)
		}
		return int(limit)
	}
	return maxPromptChars
}

func (s *server) jsonBodyBytesLimit() int64 {
	if limit := s.currentEntitlements().MaxBodyBytes; limit > 0 {
		return limit
	}
	return maxJSONBodyBytes()
}

func (s *server) jobPayloadBytesLimit() int64 {
	if limit := s.currentEntitlements().MaxBodyBytes; limit > 0 {
		return limit
	}
	return defaultMaxJobPayloadBytes
}

func (s *server) tierRateLimitDefaults() (int, int) {
	rps := defaultRateLimitRPS
	if limit := s.currentEntitlements().RequestsPerSecond; limit > 0 && limit <= int64(^uint(0)>>1) {
		rps = int(limit)
	}
	burst := rps * 2
	if burst <= 0 {
		burst = defaultRateLimitBurst
	}
	return rps, burst
}

func clampRateLimitToEntitlements(rps, burst int, entitlements licensing.Entitlements) (int, int) {
	if allowed := entitlements.RequestsPerSecond; allowed > 0 && allowed <= int64(^uint(0)>>1) {
		maxRPS := int(allowed)
		if rps > maxRPS {
			rps = maxRPS
		}
	}
	if burst <= 0 {
		burst = rps * 2
	}
	maxBurst := rps * 2
	if maxBurst > 0 && burst > maxBurst {
		burst = maxBurst
	}
	return rps, burst
}

func (s *server) connectedWorkerCount() int {
	now := time.Now().UTC()
	if workers, err := s.workersFromRedisSnapshot(); err == nil && workers != nil {
		return len(workers)
	}
	return len(s.activeWorkersSnapshot(now))
}

func (s *server) registeredWorkerCount(ctx context.Context) (int, error) {
	if s == nil || s.workerCredentialStore == nil {
		return 0, nil
	}
	records, err := s.workerCredentialStore.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, record := range records {
		if !record.Revoked() {
			count++
		}
	}
	return count, nil
}

func (s *server) effectiveWorkerCount(ctx context.Context) (registered, connected, current int, err error) {
	registered, err = s.registeredWorkerCount(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	connected = s.connectedWorkerCount()
	current = connected
	if registered > current {
		current = registered
	}
	return registered, connected, current, nil
}

func (s *server) schemaCount(ctx context.Context) (int, error) {
	if s == nil || s.schemaRegistry == nil {
		return 0, nil
	}
	ids, err := s.schemaRegistry.List(ctx, 1000)
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *server) customPolicyBundleCount(ctx context.Context) (int, error) {
	if s == nil || s.configSvc == nil {
		return 0, nil
	}
	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for bundleID := range bundles {
		if strings.HasPrefix(bundleID, policyStudioPrefix) {
			count++
		}
	}
	return count, nil
}

func (s *server) activeJobCount(ctx context.Context, tenant string) (int, error) {
	if s == nil || s.jobStore == nil || strings.TrimSpace(tenant) == "" {
		return 0, nil
	}
	return s.jobStore.CountActiveByTenant(ctx, tenant)
}

func (s *server) activeWorkflowCount(ctx context.Context, orgID string) (int, error) {
	if s == nil || s.workflowStore == nil || strings.TrimSpace(orgID) == "" {
		return 0, nil
	}
	return s.workflowStore.CountActiveRuns(ctx, orgID)
}

func (s *server) usageTenant(r *http.Request) (string, error) {
	if s == nil {
		return "", nil
	}
	requestedTenant := s.tenant
	if r != nil {
		if resolvedTenant := tenantFromRequest(r); strings.TrimSpace(resolvedTenant) != "" {
			requestedTenant = resolvedTenant
		}
	}
	if r == nil {
		return strings.TrimSpace(requestedTenant), nil
	}
	resolvedTenant, err := s.resolveTenant(r, requestedTenant)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resolvedTenant) == "" {
		return strings.TrimSpace(s.tenant), nil
	}
	return strings.TrimSpace(resolvedTenant), nil
}
