package schema

import "strings"

type EnforcementMode string

const (
	EnforcementEnforce EnforcementMode = "enforce"
	EnforcementWarn    EnforcementMode = "warn"
	EnforcementOff     EnforcementMode = "off"
)

func ParseEnforcementMode(raw string) EnforcementMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(EnforcementEnforce):
		return EnforcementEnforce
	case string(EnforcementOff):
		return EnforcementOff
	case "", string(EnforcementWarn):
		return EnforcementWarn
	default:
		return EnforcementWarn
	}
}

func (m EnforcementMode) Normalized() EnforcementMode {
	return ParseEnforcementMode(string(m))
}

func (m EnforcementMode) Enforced() bool {
	return m.Normalized() == EnforcementEnforce
}

func (m EnforcementMode) Enabled() bool {
	return m.Normalized() != EnforcementOff
}
