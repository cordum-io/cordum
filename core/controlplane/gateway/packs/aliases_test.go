package packs

import (
	"strings"
	"testing"
)

func TestValidatePackAliasesGatewayAcceptsValidEntries(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{"openclaw"},
		{"openclaw", "alt-pack"},
		{"aa", "alt_pack_v2"},
	}
	for _, aliases := range cases {
		if err := ValidatePackAliases(aliases); err != nil {
			t.Errorf("ValidatePackAliases(%v) = %v; want nil", aliases, err)
		}
	}
}

func TestValidatePackAliasesGatewayRejectsMalformed(t *testing.T) {
	cases := []string{"Openclaw", "open!claw", "1openclaw", "-openclaw", strings.Repeat("a", 32)}
	for _, alias := range cases {
		if err := ValidatePackAliases([]string{alias}); err == nil {
			t.Errorf("ValidatePackAliases([%q]) = nil; want regex error", alias)
		}
	}
}

func TestValidatePackAliasesGatewayEnforcesCap(t *testing.T) {
	tooMany := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	err := ValidatePackAliases(tooMany)
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("ValidatePackAliases(9 aliases) = %v; want at-most error", err)
	}
}

func TestPackTopicPrefixesGateway(t *testing.T) {
	prefixes := PackTopicPrefixes("cordclaw", []string{"openclaw"})
	if len(prefixes) != 2 || prefixes[0] != "job.cordclaw." || prefixes[1] != "job.openclaw." {
		t.Errorf("PackTopicPrefixes = %v; want [job.cordclaw. job.openclaw.]", prefixes)
	}
}

func TestValidatePackManifestGatewayAcceptsAliasedTopics(t *testing.T) {
	mf := &PackManifest{
		Metadata: PackMetadata{ID: "cordclaw", Version: "1.0.0", Aliases: []string{"openclaw"}},
		Topics: []PackTopic{
			{Name: "job.cordclaw.exec"},
			{Name: "job.openclaw.tool_call"},
		},
	}
	if err := ValidatePackManifest(mf); err != nil {
		t.Errorf("ValidatePackManifest(with alias) = %v; want nil", err)
	}
}

func TestValidatePackManifestGatewayBackcompat(t *testing.T) {
	mf := &PackManifest{
		Metadata: PackMetadata{ID: "cordclaw", Version: "1.0.0"},
		Topics:   []PackTopic{{Name: "job.cordclaw.exec"}},
	}
	if err := ValidatePackManifest(mf); err != nil {
		t.Errorf("ValidatePackManifest(no alias, id topic) = %v; want nil", err)
	}
	mfBad := &PackManifest{
		Metadata: PackMetadata{ID: "cordclaw", Version: "1.0.0"},
		Topics:   []PackTopic{{Name: "job.openclaw.tool_call"}},
	}
	if err := ValidatePackManifest(mfBad); err == nil {
		t.Errorf("ValidatePackManifest(no alias, openclaw topic) = nil; want error")
	}
}

func TestValidatePoolsPatchHonorsAliases(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.openclaw.tool_call": "openclaw-pool"},
	}
	if err := ValidatePoolsPatch(patch, "cordclaw", []string{"openclaw"}, nil); err != nil {
		t.Errorf("ValidatePoolsPatch(aliased topic) = %v; want nil", err)
	}
	if err := ValidatePoolsPatch(patch, "cordclaw", nil, nil); err == nil {
		t.Errorf("ValidatePoolsPatch(no aliases) = nil; want error")
	}
}

func TestValidateTimeoutsPatchHonorsAliases(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.openclaw.tool_call": map[string]any{}},
	}
	if err := ValidateTimeoutsPatch(patch, "cordclaw", []string{"openclaw"}); err != nil {
		t.Errorf("ValidateTimeoutsPatch(aliased topic) = %v; want nil", err)
	}
	if err := ValidateTimeoutsPatch(patch, "cordclaw", nil); err == nil {
		t.Errorf("ValidateTimeoutsPatch(no aliases) = nil; want error")
	}
}
