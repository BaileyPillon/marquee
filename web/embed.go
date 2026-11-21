// Package web embeds the built user interface (web/dist) into the Go binary.
package web

import "embed"

// Dist holds the built single-page app. The build step fills web/dist; the
// files are served by internal/web.
//
//go:embed all:dist
var Dist embed.FS
