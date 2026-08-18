package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectStaticExportAndServing_nextjsFastAPI(t *testing.T) {
	dir := t.TempDir()
	frontendDir := filepath.Join(dir, "frontend")
	backendDir := filepath.Join(dir, "backend")
	os.MkdirAll(frontendDir, 0755)
	os.MkdirAll(backendDir, 0755)

	os.WriteFile(filepath.Join(frontendDir, "next.config.js"), []byte("module.exports = { output: 'export', distDir: 'dist' };"), 0644)
	os.WriteFile(filepath.Join(backendDir, "main.py"), []byte("app.mount('/', StaticFiles(directory='frontend/dist'), name='static')"), 0644)

	report := DetectStaticExportAndServing(dir)
	if !report.IsClean() {
		t.Fatalf("expected clean, got: %v", report.Issues)
	}
}

func TestDetectStaticExportAndServing_missingBackendServing(t *testing.T) {
	dir := t.TempDir()
	frontendDir := filepath.Join(dir, "frontend")
	backendDir := filepath.Join(dir, "backend")
	os.MkdirAll(frontendDir, 0755)
	os.MkdirAll(backendDir, 0755)

	os.WriteFile(filepath.Join(frontendDir, "next.config.js"), []byte("module.exports = { output: 'export' };"), 0644)
	os.WriteFile(filepath.Join(backendDir, "main.py"), []byte("app = FastAPI()"), 0644)

	report := DetectStaticExportAndServing(dir)
	if report.IsClean() {
		t.Fatal("expected issues")
	}
	if report.FrontendBuildDir != "out" {
		t.Fatalf("expected build dir out, got %s", report.FrontendBuildDir)
	}
}

func TestDetectStaticExportAndServing_nestedLayout(t *testing.T) {
	dir := t.TempDir()
	frontendDir := filepath.Join(dir, "finally", "frontend")
	backendDir := filepath.Join(dir, "finally", "backend")
	os.MkdirAll(frontendDir, 0755)
	os.MkdirAll(backendDir, 0755)

	os.WriteFile(filepath.Join(frontendDir, "next.config.js"), []byte("module.exports = { output: 'export' };"), 0644)
	os.WriteFile(filepath.Join(backendDir, "main.py"), []byte("app.mount('/', StaticFiles(directory='frontend/out'), name='static')"), 0644)

	report := DetectStaticExportAndServing(dir)
	if !report.IsClean() {
		t.Fatalf("expected clean for nested layout, got: %v", report.Issues)
	}
	if !report.HasFrontendBuild || !report.HasStaticExport || !report.HasStaticServing {
		t.Fatalf("expected nested frontend/backend detected: %+v", report)
	}
}

func TestDetectStaticExportAndServing_fallbackPage(t *testing.T) {
	dir := t.TempDir()
	frontendDir := filepath.Join(dir, "frontend")
	backendDir := filepath.Join(dir, "backend")
	os.MkdirAll(frontendDir, 0755)
	os.MkdirAll(backendDir, 0755)

	os.WriteFile(filepath.Join(frontendDir, "next.config.js"), []byte("module.exports = { output: 'export' };"), 0644)
	os.WriteFile(filepath.Join(backendDir, "main.py"), []byte(`
_FALLBACK_HTML = "<html><body><h1>Finally</h1></body></html>"

@app.get("/")
def dashboard():
    index = frontend_root / "index.html"
    if index.is_file():
        return HTMLResponse(index.read_text(encoding="utf-8"))
    return HTMLResponse(_FALLBACK_HTML)
`), 0644)

	report := DetectStaticExportAndServing(dir)
	if report.IsClean() {
		t.Fatal("expected fallback-page issue")
	}
	if !report.HasFallbackPage {
		t.Fatal("expected HasFallbackPage true")
	}
	found := false
	for _, issue := range report.Issues {
		if strings.Contains(issue, "fallback") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fallback issue text, got: %v", report.Issues)
	}
}
