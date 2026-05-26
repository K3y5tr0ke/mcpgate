// Package webui embeds the compiled admin console static assets.
// Run "make build-web" (npm run build inside web/) to populate web/dist.
package webui

import "embed"

//go:embed all:dist
var FS embed.FS
