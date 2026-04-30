package edge

import (
	"embed"
	"fmt"
	"strings"
)

const (
	EdgeSessionSchemaName      = "edge_session.schema.json"
	AgentExecutionSchemaName   = "agent_execution.schema.json"
	AgentActionEventSchemaName = "agent_action_event.schema.json"
	EdgeApprovalSchemaName     = "edge_approval.schema.json"
)

//go:embed schema/*.schema.json
var edgeSchemaFS embed.FS

// Schema returns an embedded Edge JSON Schema by file name.
func Schema(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("schema name is required")
	}
	if strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("schema name %q must be a file name", name)
	}
	if !isKnownSchemaName(name) {
		return nil, fmt.Errorf("unknown edge schema %q", name)
	}
	data, err := edgeSchemaFS.ReadFile("schema/" + name)
	if err != nil {
		return nil, fmt.Errorf("read edge schema %q: %w", name, err)
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

func isKnownSchemaName(name string) bool {
	switch name {
	case EdgeSessionSchemaName, AgentExecutionSchemaName, AgentActionEventSchemaName, EdgeApprovalSchemaName:
		return true
	default:
		return false
	}
}
