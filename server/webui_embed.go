//go:build embedui

package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// webuiDist holds the statically-exported frontend (built with
// `cd web && npm run build:static`, then copied into server/webui/dist). Compiled
// into the binary only when building with `-tags embedui`. The `all:` prefix is
// required so Next's `_next/` asset dir (leading underscore) is included.
//
//go:embed all:webui/dist
var webuiDist embed.FS

// webuiHandler serves the embedded SPA. Public (no JWT) — auth is enforced
// client-side and on /api. Serves the exported per-route index.html files and
// falls back to index.html so client-side routing still resolves unknown paths.
func (s *Server) webuiHandler() http.Handler {
	root, err := fs.Sub(webuiDist, "webui/dist")
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		// 1) exact file (assets: _next/*, favicon.ico, ...)
		// 2) route dir → <p>/index.html (trailingSlash export)
		// 3) <p>.html
		// 4) SPA fallback → index.html, for page-shaped paths only
		for _, cand := range []string{p, p + "/index.html", p + ".html"} {
			if serveFileIfExists(w, r, root, cand) {
				return
			}
		}
		// A build asset that no longer exists must fail loudly. Handing the SPA
		// shell to a stale chunk request makes the browser execute HTML as
		// JavaScript and the route dies with an opaque "page couldn't load";
		// a 404 makes the real cause visible (old build, needs a reload).
		if isUIAssetPath(p) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		http.ServeFileFS(w, r, root, "index.html")
	})
}

// isUIAssetPath reports whether p asks for build output rather than a page
// route: hashed chunks under _next/, or any file with an extension.
func isUIAssetPath(p string) bool {
	if strings.HasPrefix(p, "_next/") {
		return true
	}
	return path.Ext(p) != ""
}

// setUICacheHeaders keeps route documents revalidating (so a redeploy is picked
// up on the next navigation) while pinned hashed build output stays cached.
func setUICacheHeaders(w http.ResponseWriter, name string) {
	if strings.HasPrefix(name, "_next/static/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
}

// serveFileIfExists serves name from fsys when it exists as a regular file.
func serveFileIfExists(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	st, statErr := f.Stat()
	_ = f.Close()
	if statErr != nil || st.IsDir() {
		return false
	}
	setUICacheHeaders(w, name)
	http.ServeFileFS(w, r, fsys, name)
	return true
}
