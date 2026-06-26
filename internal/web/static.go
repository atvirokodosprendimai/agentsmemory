package web

import "embed"

// staticFS holds the dashboard's static assets (the stylesheet), embedded so the
// binary is self-contained. Served read-only under /static/.
//
//go:embed static/app.css
var staticFS embed.FS
