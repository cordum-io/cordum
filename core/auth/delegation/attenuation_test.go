package delegation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type attenuationCase struct {
	name        string
	parentDepth int
	change      func(*IssueRequest)
	wantErr     error
}

func attenuationCases() []attenuationCase {
	return []attenuationCase{
		{"tenant", 1, func(r *IssueRequest) { r.Tenant = "T2" }, ErrTenantMismatch},
		{"actions", 1, func(r *IssueRequest) { r.AllowedActions = append(r.AllowedActions, "a3") }, ErrScopeExceeded},
		{"topics", 1, func(r *IssueRequest) { r.AllowedTopics = append(r.AllowedTopics, "t3") }, ErrScopeExceeded},
		{"expiry", 1, func(r *IssueRequest) { r.TTL = 20 * time.Minute }, nil},
		{"sub-delegator", 1, func(r *IssueRequest) { r.DelegatingAgentID = "other" }, ErrAudienceMismatch},
		{"depth", 3, func(*IssueRequest) {}, ErrChainTooDeep},
		{"control", 1, func(*IssueRequest) {}, nil},
	}
}

func attenuationService(t *testing.T) (*TokenService, *RedisRevocationStore) {
	t.Helper()
	service, revocations, _ := newTestTokenService(t)
	entries := make(map[string]AgentPermissions)
	for _, agent := range []string{"agent-0", "agent-1", "agent-2", "agent-3", "agent-4", "agent-5", "next", "other"} {
		entries[agent] = AgentPermissions{AllowedActions: []string{"a1", "a2", "a3"}, AllowedTopics: []string{"t1", "t2", "t3"}}
	}
	service.agentPermissions = staticAgentPermissions{entries: entries}
	return service, revocations
}

func attenuationChain(t *testing.T, service *TokenService, depth int) []VerifiedToken {
	t.Helper()
	chain := make([]VerifiedToken, 0, depth)
	parent := ""
	for i := 0; i < depth; i++ {
		req := IssueRequest{Tenant: "T", DelegatingAgentID: fmt.Sprintf("agent-%d", i), TargetAgentID: fmt.Sprintf("agent-%d", i+1),
			AllowedActions: []string{"a1", "a2"}, AllowedTopics: []string{"t1", "t2"}, TTL: 10 * time.Minute, ParentToken: parent}
		token, _, err := service.IssueDelegationToken(context.Background(), req)
		if err != nil || token == "" {
			t.Fatalf("issue depth %d: err=%v, empty=%t", i+1, err, token == "")
		}
		verified, err := service.VerifyDelegationToken(context.Background(), token, req.TargetAgentID)
		if err != nil {
			t.Fatalf("verify depth %d: %v", i+1, err)
		}
		if verified.Tenant != req.Tenant || verified.Subject != req.DelegatingAgentID || verified.Audience != req.TargetAgentID ||
			!slices.Equal(verified.AllowedActions, req.AllowedActions) || !slices.Equal(verified.AllowedTopics, req.AllowedTopics) ||
			!verified.ExpiresAt.Equal(service.now().Add(req.TTL)) || verified.ChainDepth != i+1 {
			t.Fatalf("depth %d: issued chain does not match fixture", i+1)
		}
		chain = append(chain, verified)
		parent = token
	}
	return chain
}

func attenuationChild(parent VerifiedToken) IssueRequest {
	return IssueRequest{Tenant: parent.Tenant, DelegatingAgentID: parent.Audience, TargetAgentID: "next",
		AllowedActions: slices.Clone(parent.AllowedActions), AllowedTopics: slices.Clone(parent.AllowedTopics),
		TTL: 10 * time.Minute, ParentToken: parent.Token}
}

func TestDelegationAttenuationWiderChildRefused(t *testing.T) {
	t.Setenv(envDelegationMaxDepth, "3")
	for _, row := range attenuationCases() {
		t.Run(row.name, func(t *testing.T) {
			service, _ := attenuationService(t)
			chain := attenuationChain(t, service, row.parentDepth)
			parent := chain[len(chain)-1]
			req := attenuationChild(parent)
			row.change(&req)
			token, _, err := service.IssueDelegationToken(context.Background(), req)
			if err != nil {
				if token != "" || row.name == "control" || (row.wantErr != nil && !errors.Is(err, row.wantErr)) {
					t.Fatalf("issue: err=%v, want=%v, empty=%t", err, row.wantErr, token == "")
				}
				return
			}
			if row.wantErr != nil || token == "" {
				t.Fatalf("issue: missing refusal %v or empty token", row.wantErr)
			}
			child, err := service.VerifyDelegationToken(context.Background(), token, req.TargetAgentID)
			if err != nil {
				t.Fatalf("verify child: %v", err)
			}
			if child.Tenant != parent.Tenant || child.ExpiresAt.After(parent.ExpiresAt) || child.ChainDepth != parent.ChainDepth+1 {
				t.Fatal("issued child exceeds parent authority")
			}
			for _, scopes := range [][2][]string{{child.AllowedActions, parent.AllowedActions}, {child.AllowedTopics, parent.AllowedTopics}} {
				for _, scope := range scopes[0] {
					if !slices.Contains(scopes[1], scope) {
						t.Fatalf("issued scope %q is outside parent", scope)
					}
				}
			}
		})
	}
}

func TestDelegationAttenuationDimensionsComplete(t *testing.T) {
	classified := map[string]string{
		"IssueRequest.Tenant": "case:tenant", "IssueRequest.DelegatingAgentID": "case:sub-delegator",
		"IssueRequest.TargetAgentID":  "structural:explicit recipient of the new delegation",
		"IssueRequest.AllowedActions": "case:actions", "IssueRequest.AllowedTopics": "case:topics",
		"IssueRequest.TTL": "case:expiry", "IssueRequest.ParentToken": "case:depth",
		"DelegationClaims.Tenant": "case:tenant", "DelegationClaims.AllowedActions": "case:actions",
		"DelegationClaims.AllowedTopics": "case:topics", "DelegationClaims.DelegationChain": "case:depth",
		"DelegationClaims.ChainDepth": "case:depth", "DelegationClaims.ParentTokenJTI": "case:depth",
		"DelegationClaims.RegisteredClaims": "structural:embedded JWT fields enumerated separately",
		"RegisteredClaims.Issuer":           "structural:fixed cordum issuer", "RegisteredClaims.Subject": "case:sub-delegator",
		"RegisteredClaims.Audience": "case:sub-delegator", "RegisteredClaims.ExpiresAt": "case:expiry",
		"RegisteredClaims.NotBefore": "structural:service clock at issuance", "RegisteredClaims.IssuedAt": "structural:service clock at issuance",
		"RegisteredClaims.ID":             "structural:generated token identifier",
		"AgentPermissions.AllowedActions": "case:actions", "AgentPermissions.AllowedTopics": "case:topics",
	}
	rows := make(map[string]bool)
	for _, row := range attenuationCases() {
		rows[row.name] = true
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(IssueRequest{}), reflect.TypeOf(DelegationClaims{}), reflect.TypeOf(jwt.RegisteredClaims{}), reflect.TypeOf(AgentPermissions{})} {
		for i := 0; i < typ.NumField(); i++ {
			key := typ.Name() + "." + typ.Field(i).Name
			classification, ok := classified[key]
			if !ok {
				t.Errorf("dimension added without a case: %s", key)
			}
			kind, value, found := strings.Cut(classification, ":")
			valid := (kind == "case" && rows[value]) || kind == "structural" || (kind == "open-finding" && value == "PR1-25")
			if !found || value == "" || !valid {
				t.Errorf("invalid classification: %s = %q", key, classification)
			}
			delete(classified, key)
		}
	}
	for key := range classified {
		t.Errorf("stale classification: %s", key)
	}
}

func TestDelegationAttenuationChainDepthBoundary(t *testing.T) {
	for _, row := range []struct {
		value string
		depth int
	}{{"", 3}, {"5", 5}, {"0", 3}, {"-1", 3}, {"abc", 3}} {
		t.Run("max="+row.value, func(t *testing.T) {
			t.Setenv(envDelegationMaxDepth, row.value)
			if row.value == "" {
				if err := os.Unsetenv(envDelegationMaxDepth); err != nil {
					t.Fatal(err)
				}
			}
			service, _ := attenuationService(t)
			chain := attenuationChain(t, service, row.depth)
			last := chain[len(chain)-1]
			if last.ChainDepth != row.depth {
				t.Fatalf("depth=%d, want %d", last.ChainDepth, row.depth)
			}
			for i, link := range last.DelegationChain {
				parent := ""
				if i > 0 {
					parent = chain[i-1].JTI
				}
				if link.JTI != chain[i].JTI || link.ParentJTI != parent || chain[i].ParentTokenJTI != parent {
					t.Fatalf("depth %d: incorrect parent linkage", i+1)
				}
			}
			token, _, err := service.IssueDelegationToken(context.Background(), attenuationChild(last))
			if !errors.Is(err, ErrChainTooDeep) || token != "" {
				t.Fatalf("extra depth: err=%v, empty=%t", err, token == "")
			}
			if row.depth == 5 {
				verifier := *service
				verifier.maxDepth = 3
				if _, err := verifier.VerifyDelegationToken(context.Background(), last.Token, last.Audience); !errors.Is(err, ErrChainTooDeep) {
					t.Fatalf("lower verifier limit: %v", err)
				}
			}
		})
	}
}

func TestDelegationAttenuationRevocationCascade(t *testing.T) {
	t.Setenv(envDelegationMaxDepth, "3")
	for _, position := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("revoke-%d", position), func(t *testing.T) {
			service, revocations := attenuationService(t)
			chain := attenuationChain(t, service, 3)
			if position == 0 {
				attenuationCascade(t, service, revocations, chain)
			} else if err := revocations.Revoke(context.Background(), chain[position-1].JTI, chain[position-1].ExpiresAt); err != nil {
				t.Fatal(err)
			}
			for i, token := range chain {
				verified, err := service.VerifyDelegationToken(context.Background(), token.Token, token.Audience)
				wantRevoked := position == 0 || i >= position-1
				if (wantRevoked && (!errors.Is(err, ErrRevoked) || verified.Token != "")) || (!wantRevoked && err != nil) {
					t.Fatalf("depth %d: err=%v, want revoked=%t", i+1, err, wantRevoked)
				}
			}
			parent := chain[max(0, position-1)]
			token, _, err := service.IssueDelegationToken(context.Background(), attenuationChild(parent))
			if !errors.Is(err, ErrRevoked) || token != "" {
				t.Fatalf("revoked parent: err=%v, empty=%t", err, token == "")
			}
		})
	}
}

func attenuationCascade(t *testing.T, service *TokenService, revocations *RedisRevocationStore, chain []VerifiedToken) {
	t.Helper()
	listStore := NewRedisListStoreFromClient(revocations.client)
	for _, token := range chain {
		recordDelegationToken(t, listStore, revocations, DelegationView{
			JTI: token.JTI, Tenant: token.Tenant, Subject: token.Subject, Audience: token.Audience,
			AllowedActions: token.AllowedActions, AllowedTopics: token.AllowedTopics, Chain: token.DelegationChain,
			ChainDepth: token.ChainDepth, ParentJTI: token.ParentTokenJTI, IssuedAt: token.IssuedAt, ExpiresAt: token.ExpiresAt,
		})
	}
	result, err := revocations.CascadeRevoke(context.Background(), chain[0].JTI, "test", service.now(), true)
	if err != nil || result.CascadedCount != len(chain)-1 || len(result.RevokedJTIs) != len(chain) {
		t.Fatalf("cascade: err=%v, result=%+v", err, result)
	}
	for _, token := range chain {
		revoked, err := revocations.IsRevoked(context.Background(), token.JTI)
		if err != nil || !revoked || !slices.Contains(result.RevokedJTIs, token.JTI) {
			t.Fatalf("cascade descendant: err=%v, revoked=%t", err, revoked)
		}
	}
}
