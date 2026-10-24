// Package schema embeds the JSON Schemas boards publishes, so the binary
// validates against exactly the files in this directory.
package schema

import _ "embed"

// Case is the JSON Schema for case.yaml (decision D9).
//
//go:embed case.v1.json
var Case []byte
