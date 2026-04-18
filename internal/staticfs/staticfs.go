// Package staticfs embeds the compiled frontend assets (Tailwind output, htmx)
// so the production binary ships self-contained.
package staticfs

import "embed"

//go:embed all:static
var FS embed.FS
