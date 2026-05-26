package server

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	webui "github.com/K3y5tr0ke/mcpgate/web"
)

// consoleSPAHandler returns an http.Handler that serves the embedded admin
// console SPA. All paths that do not match a real file in web/dist fall back
// to index.html so that client-side routing (TanStack Router) works correctly.
//
// chi does not modify r.URL.Path when using Route or Mount; the path relative
// to the mount point is available via chi.RouteContext(r.Context()).RoutePath.
func consoleSPAHandler() http.Handler {
	// Strip the leading "dist" prefix so the handler sees the FS root as
	// "/" → "index.html", "/assets/..." → "assets/...".
	sub, err := fs.Sub(webui.FS, "dist")
	if err != nil {
		panic("consoleSPAHandler: cannot sub embedded FS: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// chi.RouteContext.RoutePath holds the path after the mount prefix is
		// consumed (e.g. "/assets/index.js" when mounted at "/console").
		// Fall back to r.URL.Path when called outside a chi context (tests).
		routePath := chi.RouteContext(r.Context()).RoutePath
		if routePath == "" {
			routePath = r.URL.Path
		}
		filePath := strings.TrimPrefix(routePath, "/")

		if _, err := sub.Open(filePath); err != nil {
			// Path is not a real file — let the SPA router handle it.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = routePath
		fileServer.ServeHTTP(w, r2)
	})
}
