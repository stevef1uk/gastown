package agentconsole

import (
	"embed"
)

//go:embed static/index.html
var indexHTML []byte

//go:embed static
var staticFiles embed.FS
