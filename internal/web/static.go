package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
)

// staticFS holds the dashboard's static assets (the stylesheet and the passkey
// bridge script), embedded so the binary is self-contained. Served read-only
// under /static/. http.FileServer sets Content-Type from the file extension, so
// .css and .js are served with the right type.
//
//go:embed static/app.css static/passkey.js
var staticFS embed.FS

// assetVersion is a short content hash of the embedded stylesheet. It is appended
// to the <link> URL as a cache-buster (…/app.css?v=<hash>) so a redeploy whose
// CSS changed is fetched fresh instead of served stale from a browser or CDN
// cache — the cause of a deploy rendering "without CSS". The asset is compiled
// into the binary, so the hash changes exactly when the CSS does and is stable
// across restarts of the same build.
func assetVersion() string {
	b, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		return "" // never expected; an empty version just omits the ?v= buster
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// staticAssets serves the embedded assets with cache headers tuned to the
// content-hash scheme: a request carrying the hash (?v=…) names an immutable
// artifact and may be cached for a year; an unversioned request is marked
// no-cache so a stale URL can never pin an old stylesheet in a client.
func staticAssets() http.Handler {
	fileServer := http.StripPrefix("/", http.FileServer(http.FS(staticFS)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
