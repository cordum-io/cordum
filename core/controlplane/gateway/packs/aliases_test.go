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

func TestValidateAliasOwnershipRejectsInstalledNamespaceCollisions(t *testing.T) {
	installed := map[string]PackRecord{
		"openclaw": {
			ID: "openclaw",
			Manifest: PackRecordManifest{
				Metadata: PackMetadata{ID: "openclaw", Aliases: []string{"oc"}},
				Topics:   []PackTopic{{Name: "job.openclaw.exec"}, {Name: "job.oc.run"}},
			},
		},
	}
	cases := []struct {
		name    string
		packID  string
		aliases []string
		topics  []PackTopic
		want    string
	}{
		{
			name:    "alias equals installed pack id",
			packID:  "cordclaw",
			aliases: []string{"openclaw"},
			topics:  []PackTopic{{Name: "job.openclaw.exec"}},
			want:    "already owned",
		},
		{
			name:    "alias equals installed alias",
			packID:  "cordclaw",
			aliases: []string{"oc"},
			topics:  []PackTopic{{Name: "job.oc.exec"}},
			want:    "already owned",
		},
		{
			name:    "candidate id equals installed alias",
			packID:  "oc",
			aliases: nil,
			topics:  []PackTopic{{Name: "job.oc.exec"}},
			want:    "already owned",
		},
		{
			name:    "topic under requested namespace already owned",
			packID:  "cordclaw",
			aliases: []string{"oc"},
			topics:  []PackTopic{{Name: "job.cordclaw.exec"}},
			want:    "already owned",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAliasOwnership(tc.packID, tc.aliases, tc.topics, installed)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateAliasOwnership() = %v; want error containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateAliasOwnershipAllowsSamePackUpgrade(t *testing.T) {
	installed := map[string]PackRecord{
		"cordclaw": {
			ID: "cordclaw",
			Manifest: PackRecordManifest{
				Metadata: PackMetadata{ID: "cordclaw", Aliases: []string{"openclaw"}},
				Topics:   []PackTopic{{Name: "job.openclaw.exec"}},
			},
		},
	}
	err := ValidateAliasOwnership(
		"cordclaw",
		[]string{"openclaw"},
		[]PackTopic{{Name: "job.openclaw.exec"}},
		installed,
	)
	if err != nil {
		t.Fatalf("same-pack alias upgrade rejected: %v", err)
	}
}

func TestValidateAliasOwnershipRejectsAliasEqualToPackID(t *testing.T) {
	err := ValidateAliasOwnership("cordclaw", []string{"cordclaw"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicates metadata.id") {
		t.Fatalf("ValidateAliasOwnership(alias=id) = %v; want duplicate-id error", err)
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
