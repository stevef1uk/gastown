package orchestrator

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	handlerBadWebPathRE     = regexp.MustCompile(`filepath\.Join\([^)]*\.\.[^)]*["']web["']`)
	handlerStaticNoSlashRE  = regexp.MustCompile(`HandleFunc\(\s*"/static"\s*,`)
	handlerStaticSlashRE    = regexp.MustCompile(`HandleFunc\(\s*"/static/"\s*,`)
)

// handlerHasRequestURITraversalGuard reports whether handlers.go rejects ".." on the
// raw request before ServeMux path cleaning (required for GET /static/../go.mod → 404).
func handlerHasRequestURITraversalGuard(body string) bool {
	for _, field := range []string{"RequestURI", "RawPath"} {
		if !strings.Contains(body, field) {
			continue
		}
		idx := strings.Index(body, field)
		chunk := body[idx:]
		if len(chunk) > 500 {
			chunk = chunk[:500]
		}
		if strings.Contains(chunk, `".."`) || strings.Contains(chunk, `'..'`) {
			return true
		}
	}
	return false
}

// GoTestOutputSuggestsTraversalRedirect reports httptest failures where path traversal
// got a redirect (often 307) instead of 404 — typical ServeMux + /static/ mis-registration.
func GoTestOutputSuggestsTraversalRedirect(cmdOutput string) bool {
	if !goTestOutputSuggestsFailure(cmdOutput) {
		return false
	}
	lower := strings.ToLower(cmdOutput)
	if strings.Contains(lower, "traversal request returned 307") {
		return true
	}
	return strings.Contains(lower, "traversal") &&
		strings.Contains(lower, "307") &&
		strings.Contains(lower, "404")
}

// FormatHandlerTraversalRedirectHint explains how to satisfy traversal table tests.
func FormatHandlerTraversalRedirectHint(mapping WebStaticMapping) string {
	prefix := strings.TrimSpace(mapping.StaticURLPrefix)
	if prefix == "" {
		prefix = "/static"
	}
	pattern := prefix + "/"
	if !strings.HasSuffix(prefix, "/") {
		pattern = prefix + "/"
	}
	var b strings.Builder
	b.WriteString("### Handler test: path traversal (got 307, want 404)\n")
	b.WriteString("**Cause:** `net/http.ServeMux` may issue a redirect before your handler runs when the route pattern or path cleaning does not match the test (`GET /static/../go.mod`).\n")
	b.WriteString("**Fix (generic):**\n")
	b.WriteString(fmt.Sprintf("- Register static routes as **`mux.HandleFunc(%q, …)`** (trailing slash when architecture uses `/static/{file}`).\n", pattern))
	b.WriteString("- At the **start** of that handler, reject traversal on the **request** (not only the trimmed file name):\n")
	b.WriteString("  `if strings.Contains(r.URL.RequestURI(), \"..\") || strings.Contains(r.URL.RawPath, \"..\") { http.NotFound(w, r); return }`\n")
	b.WriteString(fmt.Sprintf("- Serve files from **`filepath.Join(\"web\", name)`** with `name` from `strings.TrimPrefix(r.URL.Path, %q)` — module cwd is the layout root; do **not** use `../../web`.\n", pattern))
	b.WriteString("- Do **not** switch to `HandleFunc(\"/static\", …)` without a trailing slash to “avoid redirects” — that breaks normal `GET /static/style.css` cases.\n")
	b.WriteString("- End native **EDIT:** blocks with a line containing only **`>>>>>>> REPLACE`** (never `---END EDIT---`).\n")
	return strings.TrimSpace(b.String())
}

// HandlerStaticServePatternIssues returns write-time problems in handlers.go for static routes.
func HandlerStaticServePatternIssues(body string, mapping WebStaticMapping) []string {
	var issues []string
	if handlerBadWebPathRE.MatchString(body) {
		issues = append(issues, "do not serve web/ via filepath.Join(\"..\", …) — use filepath.Join(\"web\", file) from module root")
	}
	prefix := strings.TrimSpace(mapping.StaticURLPrefix)
	if prefix == "/static" || strings.HasPrefix(prefix, "/static/") {
		if handlerStaticNoSlashRE.MatchString(body) && !handlerStaticSlashRE.MatchString(body) {
			issues = append(issues, "register static handler as mux.HandleFunc(\"/static/\", …) not \"/static\" — required for style.css and traversal tests")
		}
		if handlerStaticSlashRE.MatchString(body) && !handlerHasRequestURITraversalGuard(body) {
			issues = append(issues, "in the /static/ handler reject \"..\" on r.URL.RequestURI() or r.URL.RawPath before serving — checking only TrimPrefix(path) after redirect yields 307 on traversal tests")
		}
	}
	return issues
}
