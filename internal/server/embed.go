package server

import "embed"

//go:embed ui.html
var UIHTML string

//go:embed assets
var Assets embed.FS
