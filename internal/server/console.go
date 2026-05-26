package server

import (
	"io/fs"
	"net/http"

	webui "github.com/K3y5tr0ke/mcpgate/web"
)

// consoleSPAHandler returns an http.Handler that serves the embedded admin
// console SPA. All paths that do not match a real file in web/dist fall back
// to index.html so that client-side routing (TanStack Router) works correctly.
func consoleSPAHandler() http.Handler {
	// Strip the leading "dist" prefix from the embedded FS so the handler
	// sees "/" → "dist/index.html", "/assets/..." → "dist/assets/...".
	sub, err := fs.Sub(webui.FS, "dist")
	if err != nil {
		// This can only happen if the embed directive is wrong — panic at init time.
		panic("consoleSPAHandler: cannot sub embedded FS: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve a real static file. If the file doesn't exist, serve
		// index.html so the SPA router takes over.
		if _, err := sub.Open(r.URL.Path[1:]); err != nil {
			// Rewrite path to root so the file server serves index.html.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
