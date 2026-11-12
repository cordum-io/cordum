// Package releasequal binds source SHAs to artifact digests, gates, results and
// open exceptions. No release workflow produces or checks this record yet.
package releasequal

import _ "embed"

const SchemaName = "qualification-record.schema.json"

//go:embed qualification-record.schema.json
var recordSchema []byte

// SchemaJSON returns a copy of the embedded qualification record schema.
func SchemaJSON() []byte { return append([]byte(nil), recordSchema...) }
