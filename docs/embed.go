package docs

import "embed"

//go:embed API.md MANAGEMENT.md openapi.json
var Files embed.FS
