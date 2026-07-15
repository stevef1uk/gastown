package orchestrator

import (
	"os"
	"path/filepath"
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
	if report.FrontendBuildDir != "dist" {
		t.Fatalf("expected build dir dist, got %s", report.FrontendBuildDir)
	}
}
