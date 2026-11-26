package perfharness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Environment struct {
	Hardware      Hardware      `json:"hardware"`
	Versions      Versions      `json:"versions"`
	Configuration Configuration `json:"configuration"`
}
type Hardware struct {
	OS          string  `json:"os"`
	Arch        string  `json:"arch"`
	CPUModel    string  `json:"cpu_model"`
	LogicalCPUs int     `json:"logical_cpus"`
	MemoryBytes *uint64 `json:"memory_bytes"`
	Fingerprint string  `json:"fingerprint,omitempty"`
}
type Versions struct {
	HarnessRevision string        `json:"harness_revision"`
	GoVersion       string        `json:"go_version"`
	Target          TargetVersion `json:"target"`
}
type TargetVersion struct {
	Version string `json:"version"`
	Source  string `json:"source"`
}

func CaptureEnvironment(ctx context.Context) Environment {
	h := Hardware{OS: runtime.GOOS, Arch: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), CPUModel: "unknown"}
	var cpu, memory string
	switch runtime.GOOS {
	case "linux":
		cpu = probe(ctx, "cat", "/proc/cpuinfo")
		memory = probe(ctx, "cat", "/proc/meminfo")
	case "darwin":
		cpu = probe(ctx, "sysctl", "-n", "machdep.cpu.brand_string")
		memory = probe(ctx, "sysctl", "-n", "hw.memsize")
	case "windows":
		cpu = probe(ctx, "powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_Processor).Name")
		memory = probe(ctx, "powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory")
	}
	h.CPUModel, h.MemoryBytes = parseCPU(h.OS, cpu), parseMemory(h.OS, memory)
	h.Fingerprint = hardwareFingerprint(h)
	v := Versions{HarnessRevision: "unknown", GoVersion: runtime.Version(), Target: TargetVersion{Version: "unknown", Source: "unknown"}}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				v.HarnessRevision = setting.Value
			}
		}
	}
	return Environment{Hardware: h, Versions: v}
}

func probe(ctx context.Context, program string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.WaitDelay = 100 * time.Millisecond
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return string(output)
}

func parseCPU(source, output string) string {
	if source == "linux" {
		output = procValue(output, "model name")
	}
	output = strings.TrimSpace(output)
	if output == "" || strings.ContainsFunc(output, unicode.IsControl) {
		return "unknown"
	}
	return output
}

func parseMemory(source, output string) *uint64 {
	multiplier := uint64(1)
	if source == "linux" {
		fields := strings.Fields(procValue(output, "MemTotal"))
		if len(fields) != 2 || fields[1] != "kB" {
			return nil
		}
		output, multiplier = fields[0], 1024
	}
	n, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	if err != nil || n == 0 || n > ^uint64(0)/multiplier {
		return nil
	}
	n *= multiplier
	return &n
}

func procValue(output, key string) string {
	for _, line := range strings.Split(output, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(name) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Canonical hardware is the JSON field order above with fingerprint omitted.
func hardwareFingerprint(h Hardware) string {
	h.Fingerprint = ""
	data, err := json.Marshal(h)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
