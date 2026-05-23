package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditRequiredImplementFiles_missing(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	layout := filepath.Join(rigDir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/api/handlers_test.go"},
	}
	got := AuditRequiredImplementFiles(rigDir, v)
	if len(got) != 1 || got[0] != "missing linkshelf/internal/api/handlers_test.go" {
		t.Fatalf("got %v", got)
	}
}

func TestValidateBeadArtifactOnDisk_missing(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	v := WorkflowValidation{MinImplementationFileBytes: 10}
	err := ValidateBeadArtifactOnDisk(rigDir, "linkshelf/internal/api/handlers_test.go", v)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("got %v", err)
	}
}

func TestAuditRequiredImplementFiles_present(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	path := filepath.Join(rigDir, "linkshelf", "internal", "api")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	body := "package api\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(path, "handlers_test.go"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:                 "linkshelf",
		RequiredFiles:              []string{"linkshelf/internal/api/handlers_test.go"},
		MinImplementationFileBytes: 10,
		MinSubstantiveLines:        2,
	}
	if issues := AuditRequiredImplementFiles(rigDir, v); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}
