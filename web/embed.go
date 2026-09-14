// Package web holds the embedded web UI.
package web

import "embed"

// Files is the web UI: the app shell, the ES modules and the vendored
// libraries.
//
//go:embed index.html maintenance.html app vendor
var Files embed.FS
