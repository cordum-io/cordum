package config

import "embed"

const (
	poolsSchemaFile        = "schema/pools.schema.json"
	timeoutsSchemaFile     = "schema/timeouts.schema.json"
	safetyPolicySchemaFile = "schema/safety_policy.schema.json"
)

//go:embed schema/*.json
var configSchemaFS embed.FS
