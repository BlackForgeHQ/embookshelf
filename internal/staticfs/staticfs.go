// Package staticfs embeds the compiled React bundle so the production binary
// ships self-contained. Files land under dist/ via `npm run build`.
package staticfs

import "embed"

//go:embed all:dist
var FS embed.FS
