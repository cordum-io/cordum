package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

var errDelegationAgentRequired = errors.New("delegation token requires an authenticated agent identity")

func (s *server) applySubmitDelegation(ctx context.Context, tenant, agentID, token string, labels map[string]string, meta *pb.JobMetadata) (map[string]string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return labels, nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errDelegationAgentRequired
	}
	service, err := s.delegationTokenService()
	if err != nil {
		return nil, fmt.Errorf("delegation token service unavailable: %w", err)
	}
	verified, err := service.VerifyDelegationToken(ctx, token, agentID)
	if err != nil {
		return nil, err
	}
	if tenant != "" && verified.Tenant != "" && !strings.EqualFold(verified.Tenant, tenant) {
		return nil, fmt.Errorf("delegation token tenant mismatch")
	}
	if labels == nil {
		labels = map[string]string{}
	}
	rootIssuer, issuerChain := delegationIssuers(verified.DelegationChain)
	labels[config.LabelDelegationDepth] = strconv.Itoa(verified.ChainDepth)
	if rootIssuer != "" {
		labels[config.LabelDelegationIssuer] = rootIssuer
	}
	if len(issuerChain) > 0 {
		labels[config.LabelDelegationIssuerChain] = strings.Join(issuerChain, ",")
	}
	if len(verified.AllowedActions) > 0 {
		labels[config.LabelDelegationScope] = strings.Join(verified.AllowedActions, ",")
	}
	if verified.Subject != "" {
		labels[config.LabelDelegationSubject] = verified.Subject
	}
	if meta != nil {
		if meta.Labels == nil {
			meta.Labels = map[string]string{}
		}
		for _, key := range []string{
			config.LabelDelegationDepth,
			config.LabelDelegationIssuer,
			config.LabelDelegationIssuerChain,
			config.LabelDelegationScope,
			config.LabelDelegationSubject,
		} {
			if value := strings.TrimSpace(labels[key]); value != "" {
				meta.Labels[key] = value
			}
		}
	}
	return labels, nil
}

func submitDelegationErrorStatus(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, errDelegationAgentRequired):
		return http.StatusBadRequest, err.Error()
	default:
		if code := delegation.ErrorCode(err); code != "" {
			return http.StatusForbidden, code
		}
		if strings.Contains(strings.ToLower(err.Error()), "tenant mismatch") {
			return http.StatusForbidden, "delegation token tenant mismatch"
		}
		return http.StatusServiceUnavailable, "delegation token service unavailable"
	}
}

func delegationIssuers(chain []delegation.ChainLink) (string, []string) {
	if len(chain) == 0 {
		return "", nil
	}
	out := make([]string, 0, len(chain))
	seen := make(map[string]struct{}, len(chain))
	for _, link := range chain {
		issuer := strings.TrimSpace(link.IssuedBy)
		if issuer == "" {
			continue
		}
		key := strings.ToLower(issuer)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, issuer)
	}
	if len(out) == 0 {
		return "", nil
	}
	return out[0], out
}
