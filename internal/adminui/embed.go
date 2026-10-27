package adminui

import "embed"

//go:embed index.html app.css app.js qrcodegen.js roadmaps.js roadmaps.css
var Files embed.FS
