package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckContentNotStub_rejectsHelloHTML(t *testing.T) {
	html := `<!DOCTYPE html>
<html><body>Hello</body></html>`
	opts := StubCheckOptions{MinFileBytes: 400, MinSubstantiveLines: 3}
	err := CheckContentNotStub([]byte(html), "defender/frontend/index.html", opts)
	if err == nil {
		t.Fatal("expected stub rejection for hello html")
	}
	if !strings.Contains(err.Error(), "stub") && !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckContentNotStub_rejectsPassOnly(t *testing.T) {
	opts := StubCheckOptions{MinFileBytes: 80, MinSubstantiveLines: 3}
	err := CheckContentNotStub([]byte("def start():\n    pass\n"), "game/main.js", opts)
	if err == nil {
		t.Fatal("expected rejection for tiny pass stub")
	}
}

func TestCheckContentNotStub_acceptsSubstantiveFile(t *testing.T) {
	var b strings.Builder
	b.WriteString("package main\n\n")
	for i := 0; i < 40; i++ {
		b.WriteString("// line of real implementation work\n")
		b.WriteString("func f() int { return 1 }\n")
	}
	opts := StubCheckOptions{MinFileBytes: 400, MinSubstantiveLines: 3}
	if err := CheckContentNotStub([]byte(b.String()), "service/main.go", opts); err != nil {
		t.Fatalf("expected pass: %v", err)
	}
}

func TestValidateWorkNotStubbed_layoutTree(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testgt1", "mayor", "rig")
	layout := filepath.Join(rigDir, "defender", "frontend")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(layout, "index.html")
	if err := os.WriteFile(stub, []byte("<html><body>Hello</body></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:           "defender",
		RequiredFiles:        []string{"defender/frontend/index.html"},
		MinImplementationFileBytes: 400,
	}.WithDefaults()
	v = ClampProfileValidation(v)
	err := ValidateWorkNotStubbed(rigDir, v)
	if err == nil {
		t.Fatal("expected layout walk to reject stub html")
	}
}

func TestValidateWorkNotStubbed_testgt1Fixture(t *testing.T) {
	// Mirrors a real stub polecat output.
	html := `<!DOCTYPE html>
<html><body>Hello</body></html>`
	py := "def hello():\n    return \"Hello\"\n"
	opts := StubCheckOptionsFromValidation(WorkflowValidation{}.WithDefaults())
	for _, tc := range []struct {
		name    string
		content string
		rel     string
	}{
		{"html", html, "defender/frontend/index.html"},
		{"py", py, "defender/backend/main.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckContentNotStub([]byte(tc.content), tc.rel, opts); err == nil {
				t.Fatal("expected stub")
			}
		})
	}
}
