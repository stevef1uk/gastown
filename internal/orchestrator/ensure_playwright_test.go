package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSpec writes a minimal playwright spec file at path.
func writeSpec(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`
import { test, expect } from '@playwright/test';
test('test', async ({ page }) => {});
`), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsurePlaywrightConfigReady_createsConfigAtLayoutRoot(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	writeSpec(t, filepath.Join(rigDir, "test", "e2e", "trading.spec.ts"))

	v := WorkflowValidation{
		LayoutRoot:    ".",
		RequiredFiles: []string{"test/e2e/trading.spec.ts"},
		DevServerPort: 8000,
	}

	msg, err := EnsurePlaywrightConfigReady(dir, "myrig", v)
	if err != nil {
		t.Fatalf("EnsurePlaywrightConfigReady failed: %v", err)
	}

	// Config is written to the layout root, not the test/ dir.
	pwConfigPath := filepath.Join(rigDir, "playwright.config.ts")
	data, err := os.ReadFile(pwConfigPath)
	if err != nil {
		t.Fatalf("playwright.config.ts not created at layout root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rigDir, "test", "playwright.config.ts")); err == nil {
		t.Error("config should not be created inside test/ dir")
	}

	content := string(data)
	if !strings.Contains(content, "baseURL: 'http://localhost:8000'") {
		t.Errorf("config missing baseURL 8000: %s", content)
	}
	if !strings.Contains(content, "testDir: './test/e2e'") {
		t.Errorf("config missing testDir './test/e2e': %s", content)
	}
	if !strings.Contains(content, "webServer") {
		t.Errorf("config missing webServer: %s", content)
	}
	if msg == "" {
		t.Error("expected non-empty log message")
	}
}

func TestEnsurePlaywrightConfigReady_usesProfilePort(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	writeSpec(t, filepath.Join(rigDir, "test", "e2e", "test.spec.ts"))

	v := WorkflowValidation{
		LayoutRoot:    ".",
		RequiredFiles: []string{"test/e2e/test.spec.ts"},
		DevServerPort: 3000, // Profile specifies 3000
	}

	if _, err := EnsurePlaywrightConfigReady(dir, "myrig", v); err != nil {
		t.Fatalf("EnsurePlaywrightConfigReady failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rigDir, "playwright.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "localhost:3000") {
		t.Errorf("config should use profile port 3000: %s", string(data))
	}
}

func TestEnsurePlaywrightConfigReady_e2eInFuturePhaseCreatesConfig(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	writeSpec(t, filepath.Join(rigDir, "app", "tests", "e2e", "pages.spec.ts"))

	// Active phase has only unit tests; e2e spec lives in a later phase.
	v := WorkflowValidation{
		LayoutRoot:    "app",
		DevServerPort: 3000,
		DeliveryPhases: []DeliveryPhase{
			{ID: "unit", RequiredFiles: []string{"app/tests/unit/pages.test.ts"}},
			{ID: "e2e", RequiredFiles: []string{"app/tests/e2e/pages.spec.ts"}},
		},
		ActivePhaseIDField: "unit",
	}

	msg, err := EnsurePlaywrightConfigReady(dir, "myrig", v)
	if err != nil {
		t.Fatalf("EnsurePlaywrightConfigReady failed: %v", err)
	}
	if msg == "" {
		t.Error("expected config creation for e2e spec in future phase")
	}

	pwConfigPath := filepath.Join(rigDir, "app", "playwright.config.ts")
	data, err := os.ReadFile(pwConfigPath)
	if err != nil {
		t.Fatalf("playwright.config.ts not created at %s: %v", pwConfigPath, err)
	}
	if !strings.Contains(string(data), "testDir: './tests/e2e'") {
		t.Errorf("config should use testDir './tests/e2e': %s", string(data))
	}
	if !strings.Contains(string(data), "localhost:3000") {
		t.Errorf("config should use profile port 3000: %s", string(data))
	}
}

func TestEnsurePlaywrightConfigReady_skipsWhenNoE2E(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Only unit test files, no e2e
	v := WorkflowValidation{
		LayoutRoot:    ".",
		RequiredFiles: []string{"src/app.test.ts", "src/component.test.tsx"},
		DevServerPort: 8000,
	}

	msg, err := EnsurePlaywrightConfigReady(dir, "myrig", v)
	if err != nil {
		t.Fatalf("EnsurePlaywrightConfigReady failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(rigDir, "playwright.config.ts")); err == nil {
		t.Error("config should not be created for non-e2e files")
	}
	if msg != "" {
		t.Errorf("expected empty message for non-e2e, got: %s", msg)
	}
}

func TestEnsurePlaywrightConfigReady_skipsWhenConfigExists(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	writeSpec(t, filepath.Join(rigDir, "test", "e2e", "test.spec.ts"))

	// Existing config at layout root must not be overwritten.
	if err := os.WriteFile(filepath.Join(rigDir, "playwright.config.ts"), []byte("existing config"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsurePlaywrightConfigReady(dir, "myrig", WorkflowValidation{
		LayoutRoot:    ".",
		RequiredFiles: []string{"test/e2e/test.spec.ts"},
		DevServerPort: 8000,
	}); err != nil {
		t.Fatalf("EnsurePlaywrightConfigReady failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rigDir, "playwright.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing config" {
		t.Error("existing config was overwritten")
	}
}

func TestPlaywrightImport_resolvesFromLayoutRoot(t *testing.T) {
	dir := t.TempDir()
	layoutDir := filepath.Join(dir, "rig", "mayor", "rig", "app")
	testDir := filepath.Join(layoutDir, "test")
	if err := os.MkdirAll(filepath.Join(layoutDir, "node_modules", "@playwright", "test"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := playwrightImport(layoutDir, testDir, "test"); got != "'@playwright/test'" {
		t.Errorf("layout root has dep: got %q, want bare import", got)
	}
}

func TestPlaywrightImport_resolvesFromTestDir(t *testing.T) {
	dir := t.TempDir()
	layoutDir := filepath.Join(dir, "rig", "mayor", "rig")
	testDir := filepath.Join(layoutDir, "test")
	if err := os.MkdirAll(filepath.Join(testDir, "node_modules", "@playwright", "test"), 0755); err != nil {
		t.Fatal(err)
	}
	got := playwrightImport(layoutDir, testDir, "test")
	if want := "'./test/node_modules/@playwright/test'"; got != want {
		t.Errorf("test dir has dep: got %q, want %q", got, want)
	}
}

func TestPlaywrightImport_fallsBackToBare(t *testing.T) {
	dir := t.TempDir()
	layoutDir := filepath.Join(dir, "rig", "mayor", "rig")
	testDir := filepath.Join(layoutDir, "test")
	if got := playwrightImport(layoutDir, testDir, "test"); got != "'@playwright/test'" {
		t.Errorf("no dep installed: got %q, want bare import", got)
	}
}

func TestEnsurePlaywrightConfigReady_relativePlaywrightImport(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	writeSpec(t, filepath.Join(rigDir, "test", "e2e", "trading.spec.ts"))
	// Dep only in the test dir (e.g. finally): layout root has no node_modules.
	if err := os.MkdirAll(filepath.Join(rigDir, "test", "node_modules", "@playwright", "test"), 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsurePlaywrightConfigReady(dir, "myrig", WorkflowValidation{
		LayoutRoot:    ".",
		RequiredFiles: []string{"test/e2e/trading.spec.ts"},
		DevServerPort: 8000,
	}); err != nil {
		t.Fatalf("EnsurePlaywrightConfigReady failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rigDir, "playwright.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "from './test/node_modules/@playwright/test'") {
		t.Errorf("config should import from test dir node_modules, got:\n%s", string(data))
	}
}

func TestEnsurePlaywrightConfigReady_skipsWhenConfigExistsInTestDir(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	writeSpec(t, filepath.Join(rigDir, "test", "e2e", "test.spec.ts"))

	// Existing config in test/ (old placement) must prevent a duplicate at the layout root.
	if err := os.WriteFile(filepath.Join(rigDir, "test", "playwright.config.ts"), []byte("existing config"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsurePlaywrightConfigReady(dir, "myrig", WorkflowValidation{
		LayoutRoot:    ".",
		RequiredFiles: []string{"test/e2e/test.spec.ts"},
		DevServerPort: 8000,
	}); err != nil {
		t.Fatalf("EnsurePlaywrightConfigReady failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(rigDir, "playwright.config.ts")); err == nil {
		t.Error("duplicate config should not be created at layout root")
	}
}
