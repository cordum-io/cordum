package gateway

import (
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/infra/config"
)

func projectVerifiedDelegationContext(verified delegation.VerifiedToken) *config.DelegationContext {
	if verified.ChainDepth <= 0 || len(verified.DelegationChain) == 0 {
		return nil
	}

	issuerChain := make([]string, 0, len(verified.DelegationChain))
	for _, link := range verified.DelegationChain {
		agentID := strings.TrimSpace(link.AgentID)
		if agentID == "" {
			continue
		}
		issuerChain = append(issuerChain, agentID)
	}
	if len(issuerChain) == 0 {
		return nil
	}

	ctx := &config.DelegationContext{
		Depth:        verified.ChainDepth,
		IssuerChain:  issuerChain,
		Scope:        append([]string(nil), verified.AllowedActions...),
		RootIssuer:   issuerChain[0],
		ParentIssuer: issuerChain[len(issuerChain)-1],
		JTI:          strings.TrimSpace(verified.JTI),
		Audience:     strings.TrimSpace(verified.Audience),
	}
	if !verified.ExpiresAt.IsZero() {
		ctx.ExpiresAt = verified.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	return ctx
}

func applyDelegationContextLabels(labels map[string]string, delegationCtx *config.DelegationContext, subject string) map[string]string {
	if delegationCtx == nil {
		return labels
	}
	if labels == nil {
		labels = map[string]string{}
	}
	labels[config.LabelDelegationDepth] = strconv.Itoa(delegationCtx.Depth)
	if delegationCtx.RootIssuer != "" {
		labels[config.LabelDelegationIssuer] = delegationCtx.RootIssuer
	}
	if len(delegationCtx.IssuerChain) > 0 {
		labels[config.LabelDelegationIssuerChain] = strings.Join(delegationCtx.IssuerChain, ",")
	}
	if len(delegationCtx.Scope) > 0 {
		labels[config.LabelDelegationScope] = strings.Join(delegationCtx.Scope, ",")
	}
	if delegationCtx.ParentIssuer != "" {
		labels[config.LabelDelegationParentIssuer] = delegationCtx.ParentIssuer
	}
	if delegationCtx.JTI != "" {
		labels[config.LabelDelegationJTI] = delegationCtx.JTI
	}
	if delegationCtx.ExpiresAt != "" {
		labels[config.LabelDelegationExpiresAt] = delegationCtx.ExpiresAt
	}
	if delegationCtx.Audience != "" {
		labels[config.LabelDelegationAudience] = delegationCtx.Audience
	}
	if subject = strings.TrimSpace(subject); subject != "" {
		labels[config.LabelDelegationSubject] = subject
	}
	return labels
}
