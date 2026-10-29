package docs

import "embed"

//go:embed API.md MANAGEMENT.md USER_WORKSPACES.md openapi.json
var Files embed.FS
