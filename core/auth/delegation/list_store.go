package delegation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	delegationTokenKeyPrefix    = "delegation:token:"
	delegationByAgentKeyPrefix  = "delegation:by-agent:"
	delegationActiveKeyPrefix   = "delegation:active:"
	delegationAllKeyPrefix      = "delegation:all:"
	delegationChildrenKeyPrefix = "delegation:children:"
)

type DelegationView struct {
	JTI            string      `json:"jti"`
	Tenant         string      `json:"-"`
	Issuer         string      `json:"issuer"`
	Subject        string      `json:"subject"`
	Audience       string      `json:"audience"`
	AllowedActions []string    `json:"allowed_actions,omitempty"`
	AllowedTopics  []string    `json:"allowed_topics,omitempty"`
	Chain          []ChainLink `json:"chain,omitempty"`
	ChainDepth     int         `json:"chain_depth"`
	IssuedAt       time.Time   `json:"issued_at"`
	ExpiresAt      time.Time   `json:"expires_at"`
	Revoked        bool        `json:"revoked"`
	RevokedAt      time.Time   `json:"revoked_at,omitempty"`
	RevokedReason  string      `json:"revoked_reason,omitempty"`
	ParentJTI      string      `json:"-"`
}

type DelegationListFilter struct {
	Status       string
	Scope        string
	BeforeExpiry time.Time
	SinceIssued  time.Time
	UntilIssued  time.Time
}

type DelegationListPage struct {
	Items      []DelegationView
	NextCursor string
}

type RedisListStore struct {
	client redis.UniversalClient
}

func NewRedisListStoreFromClient(client redis.UniversalClient) *RedisListStore {
	return &RedisListStore{client: client}
}

func (s *RedisListStore) RecordIssuedToken(ctx context.Context, view DelegationView) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("delegation list store unavailable")
	}
	view.Tenant = strings.TrimSpace(view.Tenant)
	view.JTI = strings.TrimSpace(view.JTI)
	view.Subject = strings.TrimSpace(view.Subject)
	if view.Tenant == "" || view.JTI == "" || view.Subject == "" {
		return fmt.Errorf("delegation list store requires tenant, jti, and subject")
	}
	chainJSON, err := json.Marshal(view.Chain)
	if err != nil {
		return fmt.Errorf("marshal delegation chain: %w", err)
	}
	actionsJSON, err := json.Marshal(view.AllowedActions)
	if err != nil {
		return fmt.Errorf("marshal delegation actions: %w", err)
	}
	topicsJSON, err := json.Marshal(view.AllowedTopics)
	if err != nil {
		return fmt.Errorf("marshal delegation topics: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, delegationTokenKey(view.JTI), map[string]any{
		"tenant":          view.Tenant,
		"issuer":          strings.TrimSpace(view.Issuer),
		"subject":         view.Subject,
		"audience":        strings.TrimSpace(view.Audience),
		"allowed_actions": string(actionsJSON),
		"allowed_topics":  string(topicsJSON),
		"chain":           string(chainJSON),
		"chain_depth":     view.ChainDepth,
		"issued_at":       view.IssuedAt.UTC().Format(time.RFC3339Nano),
		"expires_at":      view.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"revoked":         boolToString(view.Revoked),
		"revoked_at":      formatOptionalTime(view.RevokedAt),
		"revoked_reason":  strings.TrimSpace(view.RevokedReason),
		"parent_jti":      strings.TrimSpace(view.ParentJTI),
	})
	pipe.ZAdd(ctx, delegationByAgentKey(view.Tenant, view.Subject), redis.Z{
		Score:  float64(view.IssuedAt.UTC().Unix()),
		Member: view.JTI,
	})
	pipe.ZAdd(ctx, delegationActiveKey(view.Tenant), redis.Z{
		Score:  float64(view.ExpiresAt.UTC().Unix()),
		Member: view.JTI,
	})
	pipe.ZAdd(ctx, delegationAllKey(view.Tenant), redis.Z{
		Score:  float64(view.IssuedAt.UTC().Unix()),
		Member: view.JTI,
	})
	if parentJTI := strings.TrimSpace(view.ParentJTI); parentJTI != "" {
		pipe.SAdd(ctx, delegationChildrenKey(parentJTI), view.JTI)
	}
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("record delegation token: %w", err)
	}
	return nil
}

func (s *RedisListStore) MarkRevoked(ctx context.Context, tenant, jti, reason string, revokedAt time.Time) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("delegation list store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	jti = strings.TrimSpace(jti)
	if tenant == "" || jti == "" {
		return fmt.Errorf("delegation list store requires tenant and jti")
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, delegationTokenKey(jti), map[string]any{
		"revoked":        "1",
		"revoked_at":     revokedAt.UTC().Format(time.RFC3339Nano),
		"revoked_reason": strings.TrimSpace(reason),
	})
	pipe.ZRem(ctx, delegationActiveKey(tenant), jti)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark delegation revoked: %w", err)
	}
	return nil
}

func (s *RedisListStore) ListByAgent(ctx context.Context, tenant, agentID string, filter DelegationListFilter, cursor string, limit int) (DelegationListPage, error) {
	if s == nil || s.client == nil {
		return DelegationListPage{}, fmt.Errorf("delegation list store unavailable")
	}
	jtis, err := s.client.ZRevRange(ctx, delegationByAgentKey(strings.TrimSpace(tenant), strings.TrimSpace(agentID)), 0, -1).Result()
	if err != nil {
		return DelegationListPage{}, fmt.Errorf("list delegations by agent: %w", err)
	}
	return s.pageFromJTIs(ctx, jtis, tenant, filter, cursor, limit)
}

func (s *RedisListStore) ListAll(ctx context.Context, tenant string, filter DelegationListFilter, cursor string, limit int) (DelegationListPage, error) {
	if s == nil || s.client == nil {
		return DelegationListPage{}, fmt.Errorf("delegation list store unavailable")
	}
	jtis, err := s.client.ZRevRange(ctx, delegationAllKey(strings.TrimSpace(tenant)), 0, -1).Result()
	if err != nil {
		return DelegationListPage{}, fmt.Errorf("list delegations: %w", err)
	}
	return s.pageFromJTIs(ctx, jtis, tenant, filter, cursor, limit)
}

func (s *RedisListStore) Get(ctx context.Context, jti string) (DelegationView, bool, error) {
	if s == nil || s.client == nil {
		return DelegationView{}, false, fmt.Errorf("delegation list store unavailable")
	}
	return s.loadView(ctx, jti)
}

func (s *RedisListStore) pageFromJTIs(ctx context.Context, jtis []string, tenant string, filter DelegationListFilter, cursor string, limit int) (DelegationListPage, error) {
	filtered := make([]DelegationView, 0, len(jtis))
	for _, jti := range jtis {
		view, ok, err := s.loadView(ctx, jti)
		if err != nil {
			return DelegationListPage{}, err
		}
		if !ok || !strings.EqualFold(view.Tenant, strings.TrimSpace(tenant)) {
			continue
		}
		if !matchesDelegationFilter(view, filter, time.Now().UTC()) {
			continue
		}
		filtered = append(filtered, view)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].IssuedAt.After(filtered[j].IssuedAt)
	})
	offset, err := parseDelegationCursor(cursor)
	if err != nil {
		return DelegationListPage{}, err
	}
	limit = normalizeDelegationLimit(limit)
	if offset >= len(filtered) {
		return DelegationListPage{Items: []DelegationView{}}, nil
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := DelegationListPage{
		Items: append([]DelegationView(nil), filtered[offset:end]...),
	}
	if end < len(filtered) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func (s *RedisListStore) loadView(ctx context.Context, jti string) (DelegationView, bool, error) {
	values, err := s.client.HGetAll(ctx, delegationTokenKey(strings.TrimSpace(jti))).Result()
	if err != nil {
		return DelegationView{}, false, fmt.Errorf("load delegation token %s: %w", jti, err)
	}
	if len(values) == 0 {
		return DelegationView{}, false, nil
	}
	view, err := delegationViewFromHash(strings.TrimSpace(jti), values)
	if err != nil {
		return DelegationView{}, false, fmt.Errorf("decode delegation token %s: %w", jti, err)
	}
	return view, true, nil
}

func delegationViewFromHash(jti string, values map[string]string) (DelegationView, error) {
	view := DelegationView{
		JTI:           jti,
		Tenant:        strings.TrimSpace(values["tenant"]),
		Issuer:        strings.TrimSpace(values["issuer"]),
		Subject:       strings.TrimSpace(values["subject"]),
		Audience:      strings.TrimSpace(values["audience"]),
		Revoked:       values["revoked"] == "1",
		RevokedReason: strings.TrimSpace(values["revoked_reason"]),
		ParentJTI:     strings.TrimSpace(values["parent_jti"]),
	}
	if chainDepth := strings.TrimSpace(values["chain_depth"]); chainDepth != "" {
		parsed, err := strconv.Atoi(chainDepth)
		if err != nil {
			return DelegationView{}, err
		}
		view.ChainDepth = parsed
	}
	if issuedAt := strings.TrimSpace(values["issued_at"]); issuedAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, issuedAt)
		if err != nil {
			return DelegationView{}, err
		}
		view.IssuedAt = parsed.UTC()
	}
	if expiresAt := strings.TrimSpace(values["expires_at"]); expiresAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil {
			return DelegationView{}, err
		}
		view.ExpiresAt = parsed.UTC()
	}
	if revokedAt := strings.TrimSpace(values["revoked_at"]); revokedAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, revokedAt)
		if err != nil {
			return DelegationView{}, err
		}
		view.RevokedAt = parsed.UTC()
	}
	if raw := strings.TrimSpace(values["allowed_actions"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &view.AllowedActions); err != nil {
			return DelegationView{}, err
		}
	}
	if raw := strings.TrimSpace(values["allowed_topics"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &view.AllowedTopics); err != nil {
			return DelegationView{}, err
		}
	}
	if raw := strings.TrimSpace(values["chain"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &view.Chain); err != nil {
			return DelegationView{}, err
		}
	}
	return view, nil
}

func matchesDelegationFilter(view DelegationView, filter DelegationListFilter, now time.Time) bool {
	status := strings.ToLower(strings.TrimSpace(filter.Status))
	switch status {
	case "", "all":
	case "active":
		if view.Revoked || (!view.ExpiresAt.IsZero() && view.ExpiresAt.Before(now)) {
			return false
		}
	case "revoked":
		if !view.Revoked {
			return false
		}
	case "expired":
		if view.Revoked || view.ExpiresAt.IsZero() || !view.ExpiresAt.Before(now) {
			return false
		}
	default:
		return false
	}
	if scope := strings.ToLower(strings.TrimSpace(filter.Scope)); scope != "" {
		matched := false
		for _, action := range view.AllowedActions {
			if strings.Contains(strings.ToLower(strings.TrimSpace(action)), scope) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if !filter.BeforeExpiry.IsZero() && (view.ExpiresAt.IsZero() || view.ExpiresAt.After(filter.BeforeExpiry)) {
		return false
	}
	if !filter.SinceIssued.IsZero() && view.IssuedAt.Before(filter.SinceIssued) {
		return false
	}
	if !filter.UntilIssued.IsZero() && view.IssuedAt.After(filter.UntilIssued) {
		return false
	}
	return true
}

func delegationTokenKey(jti string) string {
	return delegationTokenKeyPrefix + strings.TrimSpace(jti)
}

func delegationByAgentKey(tenant, agentID string) string {
	return delegationByAgentKeyPrefix + strings.TrimSpace(tenant) + ":" + strings.TrimSpace(agentID)
}

func delegationActiveKey(tenant string) string {
	return delegationActiveKeyPrefix + strings.TrimSpace(tenant)
}

func delegationAllKey(tenant string) string {
	return delegationAllKeyPrefix + strings.TrimSpace(tenant)
}

func delegationChildrenKey(jti string) string {
	return delegationChildrenKeyPrefix + strings.TrimSpace(jti)
}

func normalizeDelegationLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func parseDelegationCursor(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return value, nil
}

func boolToString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
