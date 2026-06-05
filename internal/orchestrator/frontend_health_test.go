package orchestrator

import (
	"strings"
	"testing"
)

func TestCheckJavaScriptFileHealthy_rejectsConcatenated(t *testing.T) {
	block := `document.addEventListener("DOMContentLoaded", () => { loadLinks(); });`
	bad := strings.Repeat(block, 3)
	if err := CheckJavaScriptFileHealthy([]byte(bad)); err == nil {
		t.Fatal("expected concatenated JS to fail")
	}
}

func TestCheckJavaScriptFileHealthy_acceptsSingleBlock(t *testing.T) {
	ok := `document.addEventListener("DOMContentLoaded", () => {
  fetch("/api/links").then(r => r.json()).then(render);
});`
	if err := CheckJavaScriptFileHealthy([]byte(ok)); err != nil {
		t.Fatalf("expected healthy JS: %v", err)
	}
}

func TestWebFileFromStaticURL(t *testing.T) {
	m := WebStaticMapping{StaticURLPrefix: "/static"}
	got := WebFileFromStaticURL("/static/app.js", m, "linkshelf")
	if got != "linkshelf/web/app.js" {
		t.Fatalf("got %q", got)
	}
}
