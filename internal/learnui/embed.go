package learnui

import "embed"

//go:embed index.html app.css app.js player.js player.css
var Files embed.FS
