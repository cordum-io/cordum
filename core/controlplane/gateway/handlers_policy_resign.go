package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/policysign"
	"github.com/redis/go-redis/v9"
)

var errResignContentChanged = errors.New("policy bundle changed during re-sign")

// Pack install and uninstall take this same lock through acquirePackLocks.
const resignPackLockResource = "packs:global"
const resignPackLockTTL = 60 * time.Second

// handleResignPolicyBundle is an explicit operator action, never called automatically.
// It requires the admin role; requireStoreAndRole admits the canonical operator
// and secops aliases through auth.NormalizeRole.
func (s *server) handleResignPolicyBundle(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndRole(w, r, []string{"admin"}, s.configSvc) {
		return
	}
	id := policybundles.BundleIDFromRequest(r)
	if id == "" {
		writeJSONError(w, 400, errorCodePolicyValidationFailed, "bundle id is required")
		return
	}
	lockOwner := packLockOwner(r)
	if !s.resignAcquireLock(w, r, lockOwner) {
		return
	}
	defer s.resignReleaseLock(lockOwner)
	owner, err := s.policyFragmentOwner(r.Context(), id)
	if err != nil {
		writeInternalError(w, r, "read policy ownership", err)
		return
	}
	if owner != "" {
		writeJSONError(w, 400, errorCodePolicyValidationFailed, fmt.Sprintf("bundle %q is pack %q's policy fragment; reinstall the pack to store a signed fragment", id, owner))
		return
	}
	signer, err := resolvePolicyStoreSigner("policy bundle re-sign " + id)
	if err != nil || signer == nil {
		message := "no policy signing key is configured"
		if err != nil {
			message = err.Error()
		}
		writeJSONError(w, 503, errorCodePolicySigningRequired, message)
		return
	}
	content, sig, ok := s.resignReadAndSign(w, r, id, signer)
	if ok {
		s.resignCommit(w, r, id, content, lockOwner, sig)
	}
}

func (s *server) resignAcquireLock(w http.ResponseWriter, r *http.Request, owner string) bool {
	if s.lockStore != nil {
		if _, ok, err := s.lockStore.Acquire(r.Context(), resignPackLockResource, owner, locks.ModeExclusive, resignPackLockTTL); err == nil && ok {
			return true
		}
	}
	writeJSONError(w, 409, errorCodePolicyVersionConflict, "a pack install or uninstall is in progress; retry the re-sign")
	return false
}

func (s *server) resignReleaseLock(owner string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := s.lockStore.Release(ctx, resignPackLockResource, owner); err != nil {
		slog.Error("policy re-sign lock release failed", "error", err)
	}
}

func (s *server) resignReadAndSign(w http.ResponseWriter, r *http.Request, id string, signer *policyStoreSigner) (string, map[string]any, bool) {
	doc, err := getConfigDoc(r.Context(), s.configSvc, packs.PolicyConfigScope, packs.PolicyConfigID)
	if errors.Is(err, redis.Nil) {
		writeErrorJSON(w, 404, "policy bundle not found")
		return "", nil, false
	}
	if err != nil {
		writeInternalError(w, r, "read policy bundle", err)
		return "", nil, false
	}
	bundles, _ := doc.Data[packs.PolicyConfigKey].(map[string]any)
	raw, exists := bundles[id]
	if !exists {
		writeErrorJSON(w, 404, "policy bundle not found")
		return "", nil, false
	}
	content := resignBundleContent(raw)
	if strings.TrimSpace(content) == "" {
		writeJSONError(w, 400, errorCodePolicyValidationFailed, "policy bundle content is empty")
		return "", nil, false
	}
	sig, signErr := signer.sign([]byte(content))
	parsed, ok := storeSourceSignature(sig)
	trust, trustErr := policysign.LoadTrustStoreFromEnv()
	pub, found := trust.Lookup(parsed.KeyID)
	if signErr != nil || !ok || trustErr != nil || !found || policysign.Verify(pub, []byte(content), parsed) != nil {
		writeJSONError(w, 503, errorCodePolicySigningRequired, "the policy signing key is not in the policy trust store")
		return "", nil, false
	}
	return content, sig, true
}

func (s *server) resignCommit(w http.ResponseWriter, r *http.Request, id, content, owner string, sig map[string]any) {
	if _, ok, err := s.lockStore.Renew(r.Context(), resignPackLockResource, owner, resignPackLockTTL); err != nil || !ok {
		writeJSONError(w, 409, errorCodePolicyVersionConflict, "the pack lock expired during re-sign; retry")
		return
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	err := s.commitPolicyWithAudit(r.Context(), func(doc *configsvc.Document) error {
		bundles, _ := doc.Data[packs.PolicyConfigKey].(map[string]any)
		raw, exists := bundles[id]
		if !exists || resignBundleContent(raw) != content {
			return errResignContentChanged
		}
		bundle, ok := raw.(map[string]any)
		if !ok {
			bundle = map[string]any{"content": content}
		}
		bundle[policyBundleSignatureKey], bundle["updated_at"] = sig, updatedAt
		bundles[id] = bundle
		return nil
	}, policybundles.PolicyAuditEntry{
		Action: "resign", ResourceType: "policy", ResourceID: id, ResourceName: id,
		ActorID: policybundles.PolicyActorID(r), Role: policybundles.PolicyRole(r), Message: "re-sign policy bundle " + id,
	})
	if errors.Is(err, errResignContentChanged) || errors.Is(err, configsvc.ErrRevisionConflict) {
		writeJSONError(w, 409, errorCodePolicyVersionConflict, "policy bundle changed during re-sign; retry")
		return
	}
	if err != nil {
		writeInternalError(w, r, "commit policy re-sign", err)
		return
	}
	s.publishConfigChanged(packs.PolicyConfigScope, packs.PolicyConfigID)
	writeJSON(w, map[string]any{"id": id, "key_id": sig["key_id"], "hash": sig["hash"], "updated_at": updatedAt})
}

func resignBundleContent(raw any) string {
	if text, ok := raw.(string); ok {
		return text
	}
	if bundle, ok := raw.(map[string]any); ok {
		for _, field := range []string{"content", "policy", "data"} {
			if text, ok := bundle[field].(string); ok {
				return text
			}
		}
	}
	return ""
}

func (s *server) policyFragmentOwner(ctx context.Context, bundleID string) (string, error) {
	records, _, err := s.loadPackRegistry(ctx)
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		for _, fragment := range records[id].Overlays.Policy {
			if fragment.FragmentID == bundleID {
				return id, nil
			}
		}
	}
	return "", err
}
