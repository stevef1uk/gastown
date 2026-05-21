package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImplementMissingFileReadNudge_activeBeadUseWrite(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "linkshelf", TestRunner: "go"}
	msg := ImplementMissingFileReadNudge("", "", "te-new", "linkshelf/internal/store/store.go", "linkshelf/internal/store/store.go", v)
	if !strings.Contains(msg, "active implement bead") || !strings.Contains(msg, "WRITE:") {
		t.Fatalf("nudge = %q", msg)
	}
}

func TestValidateImplementReadMissingFile_allowsExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	store := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(store, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "store_test.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", TestRunner: "go"}
	if err := ValidateImplementReadMissingFile(dir, rig, "te-test", "linkshelf/internal/store/store_test.go", "linkshelf/internal/store/store_test.go", v); err != nil {
		t.Fatalf("existing file should be readable: %v", err)
	}
}

func TestEnsureTestBeadSkeletonForPath_idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	v := WorkflowValidation{LayoutRoot: "linkshelf", TestRunner: "go"}
	path := "linkshelf/internal/store/store_test.go"
	_, created1, err := EnsureTestBeadSkeletonForPath(dir, rig, path, v)
	if err != nil || !created1 {
		t.Fatalf("first create: created=%v err=%v", created1, err)
	}
	_, created2, err := EnsureTestBeadSkeletonForPath(dir, rig, path, v)
	if err != nil || created2 {
		t.Fatalf("second call should not recreate: created=%v err=%v", created2, err)
	}
}

func TestEnsureTestBeadSkeletonForPath_python(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	testPath := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "tests", "test_store.py")
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		TestRunner: "pytest",
	}
	rel, created, err := EnsureTestBeadSkeletonForPath(dir, rig, "linkshelf/tests/test_store.py", v)
	if err != nil || !created || rel != "linkshelf/tests/test_store.py" {
		t.Fatalf("skeleton = %q %v %v", rel, created, err)
	}
	data, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "pytest") || !strings.Contains(string(data), "test_placeholder") {
		t.Fatalf("body = %s", data)
	}
}

func TestFormatPlanAcceptanceChecklist_defaultsWithoutPlanSection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	v := WorkflowValidation{LayoutRoot: "linkshelf", TestRunner: "go"}
	got := FormatPlanAcceptanceChecklist(dir, rig, "linkshelf/internal/store/store.go", v)
	if !strings.Contains(got, "### Acceptance checklist") {
		t.Fatalf("want defaults: %q", got)
	}
	if !strings.Contains(got, "go build") && !strings.Contains(got, "Verify") {
		t.Fatalf("want verify guidance: %q", got)
	}
}

func TestExtractImplementReadPathsFromCmd_ignoresCatRedirect(t *testing.T) {
	t.Parallel()
	paths := ExtractImplementReadPathsFromCmd("cat > linkshelf/internal/store/store.go <<'EOF'", "linkshelf")
	if len(paths) != 0 {
		t.Fatalf("cat > should not be a read path: %v", paths)
	}
}

func TestValidateImplementReadMissingFile_allowsPlanningDocs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	for _, doc := range []string{"SPEC.md", "architecture.md", "plan.md"} {
		if err := ValidateImplementReadMissingFile(dir, rig, "", "", doc, v); err != nil {
			t.Fatalf("%s should be allowed when missing: %v", doc, err)
		}
	}
}

func TestCompileErrorPathsIncludingClosedDeps_noCrossPackageNoExtras(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	got := CompileErrorPathsIncludingClosedDeps("", "", "linkshelf/cmd/server/main.go",
		[]string{"linkshelf/internal/store/store.go"}, "linkshelf/cmd/server/main.go:1:1: expected declaration", v)
	if len(got) != 1 || got[0] != "linkshelf/internal/store/store.go" {
		t.Fatalf("want only explicit error path (no closed-dep expansion without cross-package signal), got %v", got)
	}
}
