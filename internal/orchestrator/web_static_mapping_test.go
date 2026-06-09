package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWebStaticMapping_staticPrefix(t *testing.T) {
	t.Parallel()
	arch := `| Method | Path | Notes |
| GET | / | web/index.html |
| GET | /static/{file} | serves web/{file} |
`
	m := ParseWebStaticMapping(arch)
	if m.StaticURLPrefix != "/static" {
		t.Fatalf("StaticURLPrefix = %q want /static", m.StaticURLPrefix)
	}
}

func TestWebDiskPathForURLRef_staticPrefix(t *testing.T) {
	t.Parallel()
	m := WebStaticMapping{StaticURLPrefix: "/static"}
	webRoot := "/rig/linkshelf/web"
	got := m.WebDiskPathForURLRef(webRoot, "linkshelf/web/index.html", "/static/app.js")
	want := filepath.Join(webRoot, "app.js")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWebDiskPathForURLRef_rootServe(t *testing.T) {
	t.Parallel()
	m := WebStaticMapping{RootServeStatic: true}
	webRoot := "/rig/linkshelf/web"
	got := m.WebDiskPathForURLRef(webRoot, "linkshelf/web/index.html", "/app.js")
	want := filepath.Join(webRoot, "app.js")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSmokeURLForHTMLRef_staticPrefix(t *testing.T) {
	t.Parallel()
	m := WebStaticMapping{StaticURLPrefix: "/static"}
	for _, tc := range []struct {
		ref, want string
	}{
		{"app.js", "/static/app.js"},
		{"style.css", "/static/style.css"},
		{"/app.js", "/static/app.js"},
		{"/static/app.js", "/static/app.js"},
	} {
		if got := m.SmokeURLForHTMLRef(tc.ref); got != tc.want {
			t.Fatalf("ref %q: got %q want %q", tc.ref, got, tc.want)
		}
	}
}

func TestStaticRefMismatchHint(t *testing.T) {
	t.Parallel()
	m := WebStaticMapping{StaticURLPrefix: "/static"}
	if hint := m.StaticRefMismatchHint("/app.js"); hint == "" || !strings.Contains(hint, "/static/app.js") {
		t.Fatalf("hint = %q", hint)
	}
	if m.StaticRefMismatchHint("/static/app.js") != "" {
		t.Fatal("expected no hint for correct prefix")
	}
}

func TestParseWebStaticMapping_singleColumnTable(t *testing.T) {
	t.Parallel()
	arch := `| **Static UI** – ` + "`" + `/static/{file}` + "`" + ` serves web files | Handler ` + "`" + `ServeStatic` + "`" + ` |
`
	m := ParseWebStaticMapping(arch)
	if m.StaticURLPrefix != "/static" {
		t.Fatalf("StaticURLPrefix = %q want /static", m.StaticURLPrefix)
	}
}
