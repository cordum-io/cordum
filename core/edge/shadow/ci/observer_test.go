package ci_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/cordum/cordum/core/edge/shadow"
	"github.com/cordum/cordum/core/edge/shadow/ci"
)

func TestPrometheusObserver_FindingEmit_BoundedLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := ci.NewPrometheusObserver(reg, nil)

	for _, prov := range []ci.Provider{ci.ProviderGitLab, ci.ProviderJenkins, ci.ProviderBuildkite, ci.ProviderCircleCI} {
		obs.RecordFindingEmit(prov, "missing_cordum_attach", string(shadow.FindingRiskHigh))
	}
	// "weird" labels are mapped to a bounded "other" bucket — never leaked.
	obs.RecordFindingEmit(ci.ProviderGitLab, "totally_invented_signal_xyz", "weirdrisk")

	mf, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var counters []*dto.Metric
	for _, m := range mf {
		if m.GetName() == "cordum_edge_shadow_finding_emit_total" {
			counters = m.Metric
		}
	}
	if len(counters) == 0 {
		t.Fatalf("expected emit counter, got none")
	}
	seenSources := make(map[string]struct{})
	for _, c := range counters {
		for _, l := range c.Label {
			if l.GetName() == "source_type" {
				seenSources[l.GetValue()] = struct{}{}
			}
			if l.GetName() == "signal" && !isBoundedSignalLabel(l.GetValue()) {
				t.Errorf("unbounded signal label leaked: %q", l.GetValue())
			}
			if l.GetName() == "risk" && !isBoundedRiskLabel(l.GetValue()) {
				t.Errorf("unbounded risk label leaked: %q", l.GetValue())
			}
		}
	}
	for _, want := range []string{
		shadow.CIProviderGitLabCI,
		shadow.CIProviderJenkins,
		shadow.CIProviderBuildkite,
		shadow.CIProviderCircleCI,
	} {
		if _, ok := seenSources[want]; !ok {
			t.Errorf("missing source_type label value %q in emit counter", want)
		}
	}
}

func TestPrometheusObserver_OIDCVerify_BoundedResults(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := ci.NewPrometheusObserver(reg, nil)
	obs.OIDCVerifyOutcome(ci.ProviderGitLab, "ok")
	obs.OIDCVerifyOutcome(ci.ProviderJenkins, "aud_mismatch")
	obs.OIDCVerifyOutcome(ci.ProviderBuildkite, "sig_invalid")
	// arbitrary string should clamp to a bounded enum.
	obs.OIDCVerifyOutcome(ci.ProviderCircleCI, "totally-invented-result")

	mf, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, m := range mf {
		if m.GetName() != "cordum_edge_shadow_oidc_verify_total" {
			continue
		}
		for _, c := range m.Metric {
			for _, l := range c.Label {
				if l.GetName() == "result" && !isBoundedOIDCResultLabel(l.GetValue()) {
					t.Errorf("unbounded oidc result label leaked: %q", l.GetValue())
				}
				if l.GetName() == "provider" && !isBoundedProviderLabel(l.GetValue()) {
					t.Errorf("unbounded provider label leaked: %q", l.GetValue())
				}
			}
		}
	}
}

func isBoundedSignalLabel(v string) bool {
	switch v {
	case "self_hosted_runner_unlabeled", "missing_cordum_attach", "agent_config_present",
		"env_var_name_indicator", "direct_provider_endpoint", "agent_action_used", "other":
		return true
	}
	return false
}

func isBoundedRiskLabel(v string) bool {
	switch v {
	case string(shadow.FindingRiskLow), string(shadow.FindingRiskMedium),
		string(shadow.FindingRiskHigh), string(shadow.FindingRiskCritical):
		return true
	}
	return false
}

func isBoundedOIDCResultLabel(v string) bool {
	switch v {
	case "ok", "sig_invalid", "exp", "aud_mismatch", "disabled":
		return true
	}
	return false
}

func isBoundedProviderLabel(v string) bool {
	switch v {
	case shadow.CIProviderGitLabCI, shadow.CIProviderJenkins,
		shadow.CIProviderBuildkite, shadow.CIProviderCircleCI:
		return true
	}
	return strings.HasPrefix(v, "other")
}
