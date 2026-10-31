package adminui

import "embed"

//go:embed index.html app.css app.js qrcodegen.js roadmaps.js roadmaps.css user-management.js user-management.css auth-sync.js home.js home.css tasks.js tasks.css agent-access.js
var Files embed.FS
