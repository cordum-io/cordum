package main

import (
	"strings"
	"testing"
)

// TestValidatePackAliasesAcceptsValidEntries verifies the regex + cap
// behavior on the happy path.
func TestValidatePackAliasesAcceptsValidEntries(t *testing.T) {
	cases := [][]string{
		nil,                      // back-compat: no aliases is valid
		{},                       // empty slice is valid
		{"openclaw"},             // single, plain lowercase
		{"openclaw", "alt-pack"}, // hyphen
		{"aa", "alt_pack"},       // 2-char min + underscore
		{"a1", "alt-pack_v2"},    // digits + mix
		{"a1", "b2", "c3", "d4", "e5", "f6", "g7", "h8"}, // exactly maxPackAliases
	}
	for _, aliases := range cases {
		if err := validatePackAliases(aliases); err != nil {
			t.Errorf("validatePackAliases(%v) = %v; want nil", aliases, err)
		}
	}
}

// TestValidatePackAliasesRejectsMalformed enforces the alias regex.
func TestValidatePackAliasesRejectsMalformed(t *testing.T) {
	cases := []struct {
		alias string
		want  string
	}{
		{"Openclaw", "must match"},              // uppercase rejected
		{"open!claw", "must match"},             // symbol rejected
		{"1openclaw", "must match"},             // leading digit rejected
		{"-openclaw", "must match"},             // leading hyphen rejected
		{"_openclaw", "must match"},             // leading underscore rejected
		{"open claw", "must match"},             // space rejected
		{"", "empty alias"},                     // empty rejected
		{strings.Repeat("a", 32), "must match"}, // 32 chars (max is 31)
	}
	for _, tc := range cases {
		err := validatePackAliases([]string{tc.alias})
		if err == nil {
			t.Errorf("validatePackAliases([%q]) = nil; want error containing %q", tc.alias, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("validatePackAliases([%q]) = %v; want error containing %q", tc.alias, err, tc.want)
		}
	}
}

// TestValidatePackAliasesEnforcesMaxAndUnique enforces the count cap +
// dup detection.
func TestValidatePackAliasesEnforcesMaxAndUnique(t *testing.T) {
	tooMany := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} // 9 > 8
	if err := validatePackAliases(tooMany); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("validatePackAliases(9 aliases) = %v; want at-most error", err)
	}
	dup := []string{"openclaw", "openclaw"}
	if err := validatePackAliases(dup); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("validatePackAliases(dup) = %v; want duplicate error", err)
	}
}

// TestPackTopicPrefixesIncludesIDAndAliases verifies the prefix-set
// construction matches the validator's contract.
func TestPackTopicPrefixesIncludesIDAndAliases(t *testing.T) {
	prefixes := packTopicPrefixes("cordclaw", []string{"openclaw", "alt-pack"})
	want := []string{"job.cordclaw.", "job.openclaw.", "job.alt-pack."}
	if len(prefixes) != len(want) {
		t.Fatalf("packTopicPrefixes len=%d want %d (%v)", len(prefixes), len(want), prefixes)
	}
	for i, p := range prefixes {
		if p != want[i] {
			t.Errorf("packTopicPrefixes[%d]=%q want %q", i, p, want[i])
		}
	}
}

// TestValidatePackManifestAcceptsAliasedTopics covers the load-bearing
// invariant: a pack with metadata.aliases=[openclaw] validates topics
// under both job.<id>.* AND job.<alias>.*. Without aliases the strict
// prefix rule fires (back-compat).
func TestValidatePackManifestAcceptsAliasedTopics(t *testing.T) {
	mfWithAlias := &packManifest{
		Metadata: packMetadata{
			ID:      "cordclaw",
			Version: "1.0.0",
			Aliases: []string{"openclaw"},
		},
		Compatibility: packCompatibility{ProtocolVersion: 0, MinCoreVersion: "0.1.0"},
		Topics: []packTopic{
			{Name: "job.cordclaw.exec"},
			{Name: "job.openclaw.tool_call"},
		},
	}
	// ProtocolVersion gates aren't applied by validatePackManifest;
	// only the namespace check matters here.
	if err := validatePackManifest(mfWithAlias); err != nil {
		t.Errorf("validatePackManifest(with alias) = %v; want nil", err)
	}

	mfNoAlias := &packManifest{
		Metadata: packMetadata{ID: "cordclaw", Version: "1.0.0"},
		Topics:   []packTopic{{Name: "job.openclaw.tool_call"}},
	}
	if err := validatePackManifest(mfNoAlias); err == nil || !strings.Contains(err.Error(), "must be namespaced under") {
		t.Errorf("validatePackManifest(no alias, openclaw topic) = %v; want namespace error", err)
	}

	mfWrongAlias := &packManifest{
		Metadata: packMetadata{ID: "cordclaw", Version: "1.0.0", Aliases: []string{"openclaw"}},
		Topics:   []packTopic{{Name: "job.cordbar.tool_call"}}, // not id and not alias
	}
	if err := validatePackManifest(mfWrongAlias); err == nil || !strings.Contains(err.Error(), "must be namespaced under") {
		t.Errorf("validatePackManifest(cordbar topic) = %v; want namespace error", err)
	}
}

func TestValidatePackAliasOwnershipRejectsInstalledNamespaceCollisions(t *testing.T) {
	installed := map[string]packRecord{
		"openclaw": {
			ID: "openclaw",
			Manifest: packRecordManifest{
				Metadata: packMetadata{ID: "openclaw", Aliases: []string{"oc"}},
				Topics:   []packTopic{{Name: "job.openclaw.exec"}, {Name: "job.oc.run"}},
			},
		},
	}
	cases := []struct {
		name    string
		packID  string
		aliases []string
		topics  []packTopic
		want    string
	}{
		{
			name:    "alias equals installed pack id",
			packID:  "cordclaw",
			aliases: []string{"openclaw"},
			topics:  []packTopic{{Name: "job.openclaw.exec"}},
			want:    "already owned",
		},
		{
			name:    "alias equals installed alias",
			packID:  "cordclaw",
			aliases: []string{"oc"},
			topics:  []packTopic{{Name: "job.oc.exec"}},
			want:    "already owned",
		},
		{
			name:    "candidate id equals installed alias",
			packID:  "oc",
			aliases: nil,
			topics:  []packTopic{{Name: "job.oc.exec"}},
			want:    "already owned",
		},
		{
			name:    "topic under requested namespace already owned",
			packID:  "cordclaw",
			aliases: []string{"oc"},
			topics:  []packTopic{{Name: "job.cordclaw.exec"}},
			want:    "already owned",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePackAliasOwnership(tc.packID, tc.aliases, tc.topics, installed)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validatePackAliasOwnership() = %v; want error containing %q", err, tc.want)
			}
		})
	}
}

func TestValidatePackAliasOwnershipAllowsSamePackUpgrade(t *testing.T) {
	installed := map[string]packRecord{
		"cordclaw": {
			ID: "cordclaw",
			Manifest: packRecordManifest{
				Metadata: packMetadata{ID: "cordclaw", Aliases: []string{"openclaw"}},
				Topics:   []packTopic{{Name: "job.openclaw.exec"}},
			},
		},
	}
	err := validatePackAliasOwnership(
		"cordclaw",
		[]string{"openclaw"},
		[]packTopic{{Name: "job.openclaw.exec"}},
		installed,
	)
	if err != nil {
		t.Fatalf("same-pack alias upgrade rejected: %v", err)
	}
}

func TestValidatePackAliasOwnershipRejectsAliasEqualToPackID(t *testing.T) {
	err := validatePackAliasOwnership("cordclaw", []string{"cordclaw"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicates metadata.id") {
		t.Fatalf("validatePackAliasOwnership(alias=id) = %v; want duplicate-id error", err)
	}
}

// TestValidatePackManifestPreservesExistingInvariants asserts that the
// alias-aware prefix check does not weaken the other validator checks
// (id regex, version required, schema id prefix, workflow id prefix).
func TestValidatePackManifestPreservesExistingInvariants(t *testing.T) {
	// Empty id still rejected.
	mfEmptyID := &packManifest{Metadata: packMetadata{Version: "1.0"}}
	if err := validatePackManifest(mfEmptyID); err == nil {
		t.Errorf("validatePackManifest(empty id) = nil; want id-required error")
	}
	// Empty version still rejected when id is set.
	mfEmptyVer := &packManifest{Metadata: packMetadata{ID: "cordclaw"}}
	if err := validatePackManifest(mfEmptyVer); err == nil {
		t.Errorf("validatePackManifest(empty version) = nil; want version-required error")
	}
}
