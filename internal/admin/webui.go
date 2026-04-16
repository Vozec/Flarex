package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/Vozec/flarex/internal/logger"
)

// webUIDist embeds the production build of the React admin dashboard.
// The directory may be empty on a fresh clone — `make web-build` populates
// it by running `pnpm build` in /web. A committed `.gitkeep` keeps the
// directory alive.
//
//go:embed all:webui/dist
var webUIDist embed.FS

// webUIFS returns a file system rooted at webui/dist so that routing to
// "/ui/foo" resolves to "webui/dist/foo" transparently.
func webUIFS() (fs.FS, error) {
	return fs.Sub(webUIDist, "webui/dist")
}

// handleSPA serves /ui/* as a single-page app:
//  - /ui or /ui/  → serve webui/dist/index.html
//  - /ui/assets/* → serve the static asset at webui/dist/assets/...
//  - /ui/anything/else → ALSO serve index.html (client-side routing).
//
// When the embedded bundle has no index.html yet (user hasn't run
// `make web-build`), respond with a helpful placeholder instead of 404
// so the admin HTTP keeps booting.
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	subFS, err := webUIFS()
	if err != nil {
		http.Error(w, "UI bundle missing — run `make web-build`", http.StatusServiceUnavailable)
		return
	}
	// Strip /ui prefix. "/ui" → ""; "/ui/" → ""; "/ui/assets/x.js" → "assets/x.js".
	p := strings.TrimPrefix(r.URL.Path, "/ui")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		serveIndex(w, subFS)
		return
	}
	// Try the literal file.
	f, err := subFS.Open(p)
	if err == nil {
		defer f.Close()
		stat, serr := f.Stat()
		if serr == nil && !stat.IsDir() {
			http.ServeFileFS(w, r, subFS, p)
			return
		}
	}
	// SPA fallback — unknown path, serve index.html so react-router can
	// pick up the URL client-side.
	serveIndex(w, subFS)
}

func serveIndex(w http.ResponseWriter, subFS fs.FS) {
	data, err := fs.ReadFile(subFS, "index.html")
	if err != nil {
		// Empty bundle — show a helpful placeholder.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(placeholderHTML))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Tight CSP: same-origin scripts/XHR, inline styles permitted for
	// Tailwind JIT output, Google Fonts allowed for Inter + JetBrains Mono,
	// no framing, no third-party connections.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self'; "+
			"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
			"font-src 'self' https://fonts.gstatic.com; "+
			"img-src 'self' data:; "+
			"connect-src 'self'; "+
			"frame-ancestors 'none'; "+
			"base-uri 'self'; "+
			"form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(data)
}

const placeholderHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>FlareX admin — build required</title>
<style>body{font-family:-apple-system,system-ui,sans-serif;background:#0c0e12;color:#e8eaed;padding:2rem;max-width:640px;margin:auto;line-height:1.5}code{background:#1d222b;padding:2px 6px;border-radius:3px;color:#f38020}h1{color:#f38020}</style>
</head><body>
<h1>FlareX admin UI bundle missing</h1>
<p>You enabled <code>admin.ui: true</code> but the React bundle hasn't been built yet.</p>
<p>From the repo root:</p>
<pre>make web-build
make rebuild</pre>
<p>Then reload this page.</p>
</body></html>`

// logSPAStatus emits a one-line status about the embedded bundle at boot.
// Called by admin.Serve after it binds.
func (s *Server) logSPAStatus() {
	if !s.UIEnabled {
		return
	}
	subFS, err := webUIFS()
	if err != nil {
		logger.L.Warn().Err(err).Msg("admin UI: embed FS unavailable")
		return
	}
	if _, err := fs.Stat(subFS, "index.html"); err != nil {
		logger.L.Warn().Msg("admin UI enabled but no SPA bundle — run `make web-build`")
		return
	}
	logger.L.Info().Msg("admin UI enabled — dashboard served at /ui/")
}
