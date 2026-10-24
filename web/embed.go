// Package web exposes the embedded, build-free frontend.
package web

import "embed"

// FS contains the complete frontend. No Node.js or asset pipeline is involved.
//
//go:embed index.html css js assets
var FS embed.FS
