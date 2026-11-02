package adminui

import "embed"

//go:embed index.html app.css app.js qrcodegen.js roadmaps.js roadmap-import.js roadmaps.css user-management.js user-management.css auth-sync.js home.js home.css tasks.js tasks.css collection-plans.js collection-plans.css agent-access.js workspace.css
var Files embed.FS
