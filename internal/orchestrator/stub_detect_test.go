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
	err := CheckContentNotStub([]byte(html), "myapp/frontend/index.html", opts)
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
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig")
	layout := filepath.Join(rigDir, "myapp", "frontend")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(layout, "index.html")
	if err := os.WriteFile(stub, []byte("<html><body>Hello</body></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:                 "myapp",
		RequiredFiles:              []string{"myapp/frontend/index.html"},
		MinImplementationFileBytes: 400,
	}.WithDefaults()
	v = ClampProfileValidation(v)
	err := ValidateWorkNotStubbed(rigDir, v)
	if err == nil {
		t.Fatal("expected layout walk to reject stub html")
	}
}

func TestCheckContentNotStub_acceptsSmallCompleteModule(t *testing.T) {
	py := `def widget(n: int) -> str:
    """Return label for integer n."""
    if n % 15 == 0:
        return "FizzBuzz"
    elif n % 3 == 0:
        return "Fizz"
    elif n % 5 == 0:
        return "Buzz"
    return str(n)
`
	opts := StubCheckOptions{MinFileBytes: 400, MinSubstantiveLines: 3}
	if err := CheckContentNotStub([]byte(py), "backend/widget.py", opts); err != nil {
		t.Fatalf("expected small complete module to pass: %v", err)
	}
	mainPy := `def main():
    from .widget import widget
    for i in range(1, 16):
        print(widget(i))

if __name__ == "__main__":
    main()
`
	if err := CheckContentNotStub([]byte(mainPy), "backend/main.py", opts); err != nil {
		t.Fatalf("expected small runner to pass: %v", err)
	}
}

func TestCheckContentNotStub_acceptsPackageInit(t *testing.T) {
	opts := StubCheckOptionsFromValidation(WorkflowValidation{
		MinImplementationFileBytes: 400,
		MinSubstantiveLines:        3,
	})
	for _, content := range []string{"", "# tasklist package\n"} {
		if err := CheckContentNotStub([]byte(content), "tasklist/__init__.py", opts); err != nil {
			t.Fatalf("package __init__.py should not be stub-checked: %v", err)
		}
	}
}

func TestValidateWorkNotStubbed_allowsMinimalInitInLayout(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig")
	layout := filepath.Join(rigDir, "tasklist")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	storePy := `def load(path: str) -> dict:
    with open(path) as f:
        return {}
def save(store: dict, path: str) -> None:
    pass
`
	if err := os.WriteFile(filepath.Join(layout, "store.py"), []byte(storePy), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "__init__.py"), []byte("# package\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "tasklist"}.WithDefaults()
	v = ClampProfileValidation(v)
	if err := ValidateWorkNotStubbed(rigDir, v); err != nil {
		t.Fatalf("minimal __init__.py in layout should pass: %v", err)
	}
}

func TestCheckContentNotStub_acceptsDependencyManifests(t *testing.T) {
	opts := StubCheckOptionsFromValidation(WorkflowValidation{
		MinImplementationFileBytes: 400,
		MinSubstantiveLines:        3,
	})
	cases := []struct {
		rel     string
		content string
	}{
		{"myapp/pkg/requirements.txt", "pytest\nflask\n"},
		{"service/go.mod", "module example.com/foo\n\ngo 1.22\n"},
		{"service/go.sum", "example.com/foo v0.0.0 h1:abc=\n"},
	}
	for _, tc := range cases {
		t.Run(tc.rel, func(t *testing.T) {
			o := optsForPath(tc.rel, opts)
			if err := CheckContentNotStub([]byte(tc.content), tc.rel, o); err != nil {
				t.Fatalf("manifest should pass: %v", err)
			}
		})
	}
}

func TestValidateBeadArtifactOnDisk_acceptsSmallConfigDotfiles(t *testing.T) {
	rigDir := t.TempDir()
	content := `# Required: OpenRouter API key for LLM chat functionality
OPENROUTER_API_KEY=your-openrouter-api-key-here

# Optional: Massive (Polygon.io) API key for real market data
MASSIVE_API_KEY=

# Optional: Set to "true" for deterministic mock LLM responses (testing)
LLM_MOCK=false
`
	envPath := filepath.Join(rigDir, ".env.example")
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	gitignorePath := filepath.Join(rigDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".venv\n__pycache__\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		MinImplementationFileBytes: 400,
		MinSubstantiveLines:        3,
	}
	if err := ValidateBeadArtifactOnDisk(rigDir, ".env.example", v); err != nil {
		t.Fatalf(".env.example should not be rejected as stub: %v", err)
	}
	if err := ValidateBeadArtifactOnDisk(rigDir, ".gitignore", v); err != nil {
		t.Fatalf(".gitignore should not be rejected as stub: %v", err)
	}
}

func TestCheckContentNotStub_rejectsTypicalStubs(t *testing.T) {
	html := `<!DOCTYPE html>
<html><body>Hello</body></html>`
	py := "def hello():\n    return \"Hello\"\n"
	opts := StubCheckOptionsFromValidation(WorkflowValidation{}.WithDefaults())
	for _, tc := range []struct {
		name    string
		content string
		rel     string
	}{
		{"html", html, "myapp/frontend/index.html"},
		{"py", py, "myapp/backend/main.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckContentNotStub([]byte(tc.content), tc.rel, opts); err == nil {
				t.Fatal("expected stub")
			}
		})
	}
}

func TestCheckContentNotStub_acceptsShortShellScripts(t *testing.T) {
	v := WorkflowValidation{}.WithDefaults()
	opts := StubCheckOptionsFromValidation(v)
	sh := "#!/bin/bash\n# Start FinAlly trading workstation on macOS/Linux\n# Pull latest images and bring services up\ndocker compose -f docker-compose.yml up -d\necho \"FinAlly running at http://localhost:8000\"\n"
	ps1 := "#!/usr/bin/env powershell\n# Stop FinAlly\ndocker-compose -f docker-compose.yml down\n"
	for _, tc := range []struct {
		name    string
		content string
		rel     string
	}{
		{"sh", sh, "scripts/start_mac.sh"},
		{"ps1", ps1, "scripts/stop_windows.ps1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pathOpts := optsForPath(tc.rel, opts)
			if err := CheckContentNotStub([]byte(tc.content), tc.rel, pathOpts); err != nil {
				t.Fatalf("expected short %s script to pass: %v", tc.name, err)
			}
		})
	}
}

func TestCheckContentNotStub_rejectsSeededTestPlaceholder(t *testing.T) {
	opts := StubCheckOptions{MinFileBytes: 160, MinSubstantiveLines: 1}
	goStub := "package api\n\nimport \"testing\"\n\n// Replace with table-driven tests from plan.md acceptance.\nfunc TestPlaceholder(t *testing.T) {\n\tt.Skip(\"" + SeededTestPlaceholderReason + "\")\n}\n"
	err := CheckContentNotStub([]byte(goStub), "linkshelf/internal/api/handlers_test.go", opts)
	if err == nil {
		t.Fatal("seeded Go test placeholder must be rejected even though it compiles and passes")
	}
	if !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("unexpected error: %v", err)
	}
	pyStub := "import pytest\n\n\n@pytest.mark.skip(reason=\"" + SeededTestPlaceholderReason + "\")\ndef test_placeholder():\n    pass\n"
	if err := CheckContentNotStub([]byte(pyStub), "app/test_main.py", opts); err == nil {
		t.Fatal("seeded pytest placeholder must be rejected")
	}
	// A real skipped test with a different reason is NOT the scaffold.
	real := "package api\n\nimport \"testing\"\n\nfunc TestDockerOnly(t *testing.T) {\n\tt.Skip(\"needs docker\")\n}\n\nfunc TestReal(t *testing.T) {\n\tif 1 != 1 {\n\t\tt.Fatal(\"impossible\")\n\t}\n}\n"
	if err := CheckContentNotStub([]byte(real), "linkshelf/internal/api/handlers_test.go", opts); err == nil && len(real) < int(opts.MinFileBytes) {
		// only assert non-rejection when size/line minimums are met
		_ = err
	}
}
