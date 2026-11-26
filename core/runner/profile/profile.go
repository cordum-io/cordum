// Package profile describes managed Linux runner requirements. Nothing in this
// package enforces them; admission, host probing and enforcement are separate.
package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

const SchemaVersion = 1

var (
	ErrUnknownField       = errors.New("unknown profile field")
	ErrUnsupportedVersion = errors.New("unsupported profile schema version")
	ErrInvalidSandbox     = errors.New("invalid sandbox constraints")
)

type NetworkMode string

const (
	NetworkNone      NetworkMode = "none"
	NetworkAllowlist NetworkMode = "allowlist"
)

type Profile struct {
	SchemaVersion uint32     `json:"schema_version"`
	Filesystem    Filesystem `json:"filesystem"`
	Network       Network    `json:"network"`
	Resources     Resources  `json:"resources"`
	Processes     Processes  `json:"processes"`
	Syscalls      Syscalls   `json:"syscalls"`
	Runtime       Runtime    `json:"runtime"`
}

type Filesystem struct {
	ReadOnlyInputs  []string `json:"read_only_inputs"`
	WorkspacePaths  []string `json:"workspace_paths"`
	WorkspaceBytes  int64    `json:"workspace_bytes"`
	WorkspaceInodes int64    `json:"workspace_inodes"`
}

type Network struct {
	Mode  NetworkMode `json:"mode"`
	Allow []string    `json:"allow"`
}

type Resources struct {
	CPUMillicores int64 `json:"cpu_millicores"`
	MemoryBytes   int64 `json:"memory_bytes"`
	SwapBytes     int64 `json:"swap_bytes"`
}

type Processes struct {
	HostPidsMax      int64 `json:"host_pids_max"`
	GuestTaskCeiling int64 `json:"guest_task_ceiling"`
}

type Syscalls struct {
	PolicyID string `json:"policy_id"`
}

type Runtime struct {
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	Release      string `json:"release"`
	BinaryPath   string `json:"binary_path"`
	BinarySHA256 string `json:"binary_sha256"`
}

// Parse decodes the schema, not admission policy or host capability evidence.
func Parse(data []byte) (Profile, error) {
	var p Profile
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			return Profile{}, fmt.Errorf("%w: %v", ErrUnknownField, err)
		}
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	var trailing json.RawMessage
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return Profile{}, fmt.Errorf("profile trailing data: %w", err)
		}
		return Profile{}, errors.New("profile trailing data")
	}
	if p.SchemaVersion != SchemaVersion {
		return Profile{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, p.SchemaVersion)
	}
	return p, nil
}

// FromSandbox maps Isolated to the always-on Requirements floor,
// NetworkAllowlist to network.allow, FsReadOnly to filesystem.read_only_inputs,
// and FsReadWrite to filesystem.workspace_paths. Paths use Linux semantics.
func FromSandbox(s config.SandboxProfile) (Profile, error) {
	for _, paths := range [][]string{s.FsReadOnly, s.FsReadWrite} {
		for _, p := range paths {
			if !path.IsAbs(p) || path.Clean(p) != p || strings.ContainsRune(p, '\x00') {
				return Profile{}, fmt.Errorf("%w: path must be absolute and clean", ErrInvalidSandbox)
			}
		}
	}
	for _, entry := range s.NetworkAllowlist {
		if entry == "" || strings.TrimSpace(entry) != entry || strings.ContainsAny(entry, "*?[]") {
			return Profile{}, fmt.Errorf("%w: empty, padded or wildcard network entry", ErrInvalidSandbox)
		}
	}
	p := Profile{SchemaVersion: SchemaVersion, Runtime: Runtime{Name: "runsc", Platform: "systrap"},
		Filesystem: Filesystem{ReadOnlyInputs: slices.Clone(s.FsReadOnly), WorkspacePaths: slices.Clone(s.FsReadWrite)},
		Network:    Network{Mode: NetworkNone, Allow: slices.Clone(s.NetworkAllowlist)}}
	if len(s.NetworkAllowlist) > 0 {
		p.Network.Mode = NetworkAllowlist
	}
	return p, nil
}
