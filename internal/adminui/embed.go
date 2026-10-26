package adminui

import "embed"

//go:embed index.html app.css app.js qrcodegen.js
var Files embed.FS
