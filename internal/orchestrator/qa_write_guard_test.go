package orchestrator

import "testing"

func linkshelfProfile() WorkflowValidation {
	return WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/web/app.js",
			"linkshelf/internal/api/handlers.go",
		},
	}
}

func TestQACommandMutatesLayoutSource_sed(t *testing.T) {
	t.Parallel()
	v := linkshelfProfile()
	cmd := `cd testrig/mayor/rig && sed -i 's|/static/app.js|/app.js|' linkshelf/web/index.html`
	if path, ok := QACommandMutatesLayoutSource(cmd, v); !ok || path == "" {
		t.Fatalf("expected block, got path=%q ok=%v", path, ok)
	}
}

func TestQACommandMutatesLayoutSource_allowsRead(t *testing.T) {
	t.Parallel()
	v := linkshelfProfile()
	cmd := `cd testrig/mayor/rig && head -n 20 linkshelf/web/index.html`
	if _, ok := QACommandMutatesLayoutSource(cmd, v); ok {
		t.Fatal("expected read-only head to be allowed")
	}
}
