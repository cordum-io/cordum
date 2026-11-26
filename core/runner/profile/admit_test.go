package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/bits"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/stretchr/testify/require"
)

type fakeProber struct {
	res   ProbeResult
	err   error
	calls int
}

func (f *fakeProber) Probe(context.Context, Profile) (ProbeResult, error) {
	f.calls++
	return f.res, f.err
}

// fullProfile routes network through an allowlist, so it requires every capability.
func fullProfile(t *testing.T) Profile {
	t.Helper()
	p, err := FromSandbox(config.SandboxProfile{Isolated: true, NetworkAllowlist: []string{"api.example.com:443"},
		FsReadOnly: []string{"/inputs"}, FsReadWrite: []string{"/workspace"}})
	require.NoError(t, err)
	require.Equal(t, AllCapabilities(), p.Requirements())
	return p
}

func allAvailable() ProbeResult {
	res := ProbeResult{ProbedAt: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC), Host: "fake/amd64", Capabilities: map[Capability]CapabilityResult{}}
	for _, c := range AllCapabilities() {
		res.Capabilities[c] = CapabilityResult{Status: StatusAvailable, Detail: "fake " + string(c)}
	}
	return res
}

// requireRefused checks that err is a refusal and that no run was admitted.
func requireRefused(t *testing.T, adm Admission, err error) *RefusalError {
	t.Helper()
	var re *RefusalError
	require.ErrorAs(t, err, &re)
	require.Zero(t, adm, "a refusal must not return an Admission")
	return re
}

func TestAdmitRefusesEachMissingCapability(t *testing.T) {
	p := fullProfile(t)
	cases := []struct {
		name   string
		result *CapabilityResult // nil: absent from the probe result
	}{
		{"unavailable", &CapabilityResult{Status: StatusUnavailable, Detail: "fake off"}},
		{"unproven", &CapabilityResult{Status: StatusUnproven, Detail: "fake no evidence"}},
		{"unknown-status", &CapabilityResult{Status: "maybe", Detail: "fake ?"}},
		{"absent", nil},
	}
	for _, c := range p.Requirements() {
		for _, tc := range cases {
			t.Run(string(c)+"/"+tc.name, func(t *testing.T) {
				res := allAvailable()
				want := MissingCapability{Capability: c, Detail: "absent from probe result"}
				if tc.result == nil {
					delete(res.Capabilities, c)
				} else {
					res.Capabilities[c] = *tc.result
					want = MissingCapability{Capability: c, Status: tc.result.Status, Detail: tc.result.Detail}
				}
				adm, err := Admit(context.Background(), p, &fakeProber{res: res})
				re := requireRefused(t, adm, err)
				require.Equal(t, []MissingCapability{want}, re.Missing)
				require.NoError(t, re.Err)
				require.Contains(t, err.Error(), string(c))
			})
		}
	}
}

func TestAdmitRefusesProbeError(t *testing.T) {
	probeErr := errors.New("fake probe failed")
	adm, err := Admit(context.Background(), fullProfile(t), &fakeProber{res: allAvailable(), err: probeErr})
	re := requireRefused(t, adm, err)
	require.ErrorIs(t, err, probeErr)
	require.Empty(t, re.Missing)
}

func TestAdmitRefusesUnknownNetworkMode(t *testing.T) {
	for _, mode := range []NetworkMode{"", "open"} {
		p := fullProfile(t)
		p.Network.Mode = mode
		adm, err := Admit(context.Background(), p, &fakeProber{res: allAvailable()})
		re := requireRefused(t, adm, err)
		require.ErrorContains(t, re.Err, "unsupported network mode", mode)
	}
}

// Over every available/unavailable combination of the requirements, only the
// full set admits, and the admitted run carries the requested profile unchanged.
func TestAdmitOnlyWhenEveryRequirementAvailable(t *testing.T) {
	p := fullProfile(t)
	req := p.Requirements()
	res := allAvailable()
	full := uint(1)<<len(req) - 1
	for mask := uint(0); mask <= full; mask++ {
		for i, c := range req {
			st := StatusUnavailable
			if mask&(1<<i) != 0 {
				st = StatusAvailable
			}
			res.Capabilities[c] = CapabilityResult{Status: st, Detail: "fake"}
		}
		adm, err := Admit(context.Background(), p, &fakeProber{res: res})
		if mask < full {
			var re *RefusalError
			if !errors.As(err, &re) || !reflect.DeepEqual(adm, Admission{}) || len(re.Missing) != len(req)-bits.OnesCount(mask) {
				t.Fatalf("mask %b: want a refusal naming every unavailable requirement, got %+v, %v", mask, adm, err)
			}
			continue
		}
		require.NoError(t, err)
		require.Equal(t, p, adm.Profile)
		require.Equal(t, req, adm.Required)
		require.Equal(t, res, adm.Probe)
		b, err := json.Marshal(p)
		require.NoError(t, err)
		sum := sha256.Sum256(b)
		require.Equal(t, hex.EncodeToString(sum[:]), adm.ProfileDigest)
		require.WithinDuration(t, time.Now(), adm.AdmittedAt, time.Minute)
	}
}

func TestAdmitRecordsProbeResult(t *testing.T) {
	p := fullProfile(t)
	f := &fakeProber{res: allAvailable()}
	adm, err := Admit(context.Background(), p, f)
	require.NoError(t, err)
	require.Equal(t, f.res, adm.Probe)
	b, err := json.Marshal(adm)
	require.NoError(t, err)
	var got Admission
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, f.res, got.Probe)
	require.Equal(t, adm.ProfileDigest, got.ProfileDigest)

	f.res = allAvailable()
	f.res.Capabilities[CapCgroupKill] = CapabilityResult{Status: StatusUnproven, Detail: "fake"}
	adm, err = Admit(context.Background(), p, f)
	re := requireRefused(t, adm, err)
	require.Equal(t, f.res, re.Probe)
	require.Equal(t, 2, f.calls, "every Admit probes afresh")
}

func TestAdmitHostProbeRefuses(t *testing.T) {
	adm, err := Admit(context.Background(), fullProfile(t), HostProber{})
	re := requireRefused(t, adm, err)
	t.Log(err)
	var names []Capability
	for _, m := range re.Missing {
		names = append(names, m.Capability)
	}
	require.Contains(t, names, CapGuestTaskCeiling)
	require.True(t, slices.IsSorted(names), names)
	require.Len(t, re.Probe.Capabilities, len(AllCapabilities()))
	for _, c := range AllCapabilities() {
		require.NotEmpty(t, re.Probe.Capabilities[c].Status, c)
	}
}
