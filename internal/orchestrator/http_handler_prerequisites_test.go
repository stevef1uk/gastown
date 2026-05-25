package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func handlerPrereqProfile() WorkflowValidation {
	return WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/web/index.html",
			"linkshelf/web/app.js",
			"linkshelf/web/style.css",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/internal/api/handlers_test.go",
			"linkshelf/cmd/server/main.go",
		},
	}
}

func TestOrderRequiredFiles_webBeforeHandlers(t *testing.T) {
	t.Parallel()
	files := []string{
		"linkshelf/cmd/server/main.go",
		"linkshelf/internal/api/handlers.go",
		"linkshelf/web/index.html",
		"linkshelf/internal/store/store.go",
	}
	got := OrderRequiredFilesForImplementation(files)
	var webIdx, handlerIdx = -1, -1
	for i, p := range got {
		if strings.Contains(p, "/web/") {
			webIdx = i
		}
		if strings.Contains(p, "/internal/api/handlers.go") {
			handlerIdx = i
		}
	}
	if webIdx < 0 || handlerIdx < 0 || webIdx > handlerIdx {
		t.Fatalf("web must be ordered before handlers: %v", got)
	}
}

func TestValidateHTTPHandlerBeadPrerequisites_blocksWhenWebMissing(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	v := handlerPrereqProfile()
	err := ValidateHTTPHandlerBeadPrerequisites(rigDir, "linkshelf/internal/api/handlers.go", v)
	if err == nil || !strings.Contains(err.Error(), "web/") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateHTTPHandlerBeadPrerequisites_allowsWhenWebPresent(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	layout := filepath.Join(rigDir, "linkshelf", "web")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "app.js", "style.css"} {
		if err := os.WriteFile(filepath.Join(layout, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := handlerPrereqProfile()
	if err := ValidateHTTPHandlerBeadPrerequisites(rigDir, "linkshelf/internal/api/handlers.go", v); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestFormatHandlerStatic404Hint_missingWeb(t *testing.T) {
	t.Parallel()
	out := `--- FAIL: TestServeIndex (0.00s)
    handlers_test.go:72: expected 200, got 404`
	v := handlerPrereqProfile()
	hint := FormatHandlerStatic404Hint(t.TempDir(), "rig", "linkshelf/internal/api/handlers.go", out, v)
	for _, want := range []string{"missing", "web/", "index.html"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("missing %q in:\n%s", want, hint)
		}
	}
	if strings.Contains(hint, "../../web") {
		t.Fatalf("should not suggest path hacks: %s", hint)
	}
}

func TestGoTestOutputSuggestsHandlerStatic404(t *testing.T) {
	t.Parallel()
	out := "--- FAIL: TestServeStatic (0.00s)\n    handlers_test.go:91: expected 200, got 404\n"
	if !GoTestOutputSuggestsHandlerStatic404(out) {
		t.Fatal("expected true")
	}
}
