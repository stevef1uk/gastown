package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsE2ETestPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"test/e2e/trading_flow.spec.ts", true},
		{"test/e2e/smoke.cy.js", true},
		{"src/App.test.js", true},
		{"playwright.config.ts", true},
		{"cypress.config.js", true},
		{"internal/store/store_test.go", false},
		{"internal/api/handlers.go", false},
		{"test/docker-compose.test.yml", false},
	}
	for _, c := range cases {
		got := IsE2ETestPath(c.path)
		if got != c.want {
			t.Errorf("IsE2ETestPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestFormatE2ETestBeadChecklist(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte("Test against http://localhost:3000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte("E2E selector: '#trade-submit'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "index.html"), []byte(`<input id="trade-input">`), 0644); err != nil {
		t.Fatal(err)
	}

	got := FormatE2ETestBeadChecklist(rigDir, "test/e2e/trading_flow.spec.ts", WorkflowValidation{})
	if got == "" {
		t.Fatal("expected non-empty checklist")
	}
	for _, want := range []string{"localhost:3000", "#trade-submit", "#trade-input"} {
		if !strings.Contains(got, want) {
			t.Errorf("checklist missing %q:\n%s", want, got)
		}
	}
}
