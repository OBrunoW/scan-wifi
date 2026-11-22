package web

import "embed"

//go:embed index.html js models
var FS embed.FS
