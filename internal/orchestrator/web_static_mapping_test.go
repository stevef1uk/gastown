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
