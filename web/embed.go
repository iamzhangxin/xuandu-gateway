package web

import "embed"

// Assets contains the production management console.
//
//go:embed dist
var Assets embed.FS
