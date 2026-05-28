package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCorruptStoreGo(t *testing.T, dir, rig string) string {
	t.Helper()
	rel := "linkshelf/internal/store/store.go"
	abs := filepath.Join(dir, rig, "mayor", "rig", rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	body := "package store\n\n" + strings.Repeat("// pad\n", 40) + "}n err\n"
	if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestValidateImplementWritePath_allowsFullWriteWhenGoCorrupt(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rel := writeCorruptStoreGo(t, dir, rig)
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.TestRunner = "go"
	v.RequiredFiles = []string{rel}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []PlanBead{{
				ID:    "te-store",
				Title: "Implement linkshelf/internal/store/store.go per architecture",
			}}, nil
		}
		return nil, nil
	})
	if PreferIncrementalEdit(dir, rig, rel, v) {
		t.Fatal("corrupt Go must not prefer incremental edit")
	}
	if err := ValidateImplementWritePath(dir, rig, "te-store", rel, v, true, "", nil); err != nil {
		t.Fatalf("full WRITE should be allowed on corrupt file: %v", err)
	}
}

func TestFormatIncrementalEditBlock_corruptedGoFile(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rel := writeCorruptStoreGo(t, dir, rig)
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.TestRunner = "go"
	got := FormatIncrementalEditBlock(dir, rig, rel, v)
	if got == "" {
		t.Fatal("expected corrupted-file block")
	}
	for _, want := range []string{"full WRITE", "not valid Go", rel} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatImplementBeadContextForPath_includesSpecStoreContract(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	rel := "linkshelf/internal/store/store.go"
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "internal", "store"), 0755); err != nil {
		t.Fatal(err)
	}
	spec := "## Store\n\n```go\nfunc (s *Store) List(ctx context.Context) ([]Link, error)\nfunc (s *Store) Create(ctx context.Context, title, url string) (Link, error)\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		TestRunner:    "go",
		RequiredFiles: []string{rel, "linkshelf/internal/store/store_test.go"},
	}
	got := formatImplementBeadContextForPath(dir, rig, rel, v)
	for _, want := range []string{
		"Store package (from SPEC.md)",
		"same names/signatures",
		":memory:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatImplementBeadContextForPath_storeTestBeadChecklist(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	rel := "linkshelf/internal/store/store_test.go"
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		TestRunner:    "go",
		RequiredFiles: []string{rel},
	}
	got := formatImplementBeadContextForPath(dir, rig, rel, v)
	if !strings.Contains(got, "### store_test.go") {
		t.Fatalf("missing store test guidance:\n%s", got)
	}
}

func TestRejectFullFileHeredocReason_allowsHeredocWhenGoCorrupt(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rel := writeCorruptStoreGo(t, dir, rig)
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.TestRunner = "go"
	v.RequiredFiles = []string{rel}
	cmd := `cat > linkshelf/internal/store/store.go <<'EOF'
package store
EOF`
	if reason := RejectFullFileHeredocReason(cmd, dir, rig, "te-store", v); reason != "" {
		t.Fatalf("corrupt file should allow heredoc recovery, got %q", reason)
	}
}
