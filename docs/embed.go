package docs

import "embed"

//go:embed API.md openapi.json
var Files embed.FS
