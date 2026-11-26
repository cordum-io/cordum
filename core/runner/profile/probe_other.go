//go:build !linux

package profile

import (
	"context"
	"runtime"
	"time"
)

// Probe reports every capability unavailable: the runner plane is Linux-only.
func (h HostProber) Probe(ctx context.Context, _ Profile) (ProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	res := ProbeResult{ProbedAt: time.Now().UTC(), Host: runtime.GOOS + "/" + runtime.GOARCH, Capabilities: map[Capability]CapabilityResult{}}
	for _, c := range AllCapabilities() {
		res.Capabilities[c] = CapabilityResult{Status: StatusUnavailable, Detail: "not linux (GOOS=" + runtime.GOOS + ")"}
	}
	return res, nil
}
