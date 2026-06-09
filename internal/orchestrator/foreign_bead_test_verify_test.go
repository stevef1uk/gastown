package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoCompileVerifyCommandForBead_schemaUsesBuildWhenForeignStoreTestBroken(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "linkshelf/internal/store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema.go"), []byte("package store\nfunc InitSchema() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema_test.go"), []byte("package store\nfunc TestInitSchema(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Broken store_test from a later bead — must not force schema verify through go test.
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store_test\nfunc broken(t *testing.T) { _ = timestampsApproximatelyEqual }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/schema.go")
	if !strings.Contains(got, "go build") {
		t.Fatalf("want go build for schema bead, got %q", got)
	}
}

func TestAllowForeignOpenBeadCompileFixForVerifyFailure_schemaBlockedByStoreTest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testgt3"
	storeDir := filepath.Join(dir, rig, "mayor", "rig", "linkshelf/internal/store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store_test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		TestRunner:        "go",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "in_progress":
			return []PlanBead{{ID: "te-bol", Title: "Implement linkshelf/internal/store/schema.go per architecture"}}, nil
		case "open":
			return []PlanBead{{ID: "te-3xo", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		default:
			return nil, nil
		}
	})
	active := "linkshelf/internal/store/schema.go"
	written := "linkshelf/internal/store/store_test.go"
	out := "# linkshelf/internal/store_test [linkshelf/internal/store.test]\ninternal/store/store_test.go:113:5: undefined: timestampsApproximatelyEqual\nFAIL\tlinkshelf/internal/store [build failed]\n"
	if !AllowForeignOpenBeadCompileFixForVerifyFailure(dir, rig, active, written, out, v) {
		t.Fatal("expected allow compile fix on foreign store_test.go")
	}
	if err := ValidateImplementWritePath(dir, rig, "te-bol", written, v, false, out, nil); err != nil {
		t.Fatalf("ValidateImplementWritePath: %v", err)
	}
	if err := ValidateImplementReadPath(dir, rig, "te-bol", written, v, out); err != nil {
		t.Fatalf("ValidateImplementReadPath: %v", err)
	}
}

func TestFormatForeignOpenBeadTestCompileHint(t *testing.T) {
	dir := t.TempDir()
	rig := "testgt3"
	storeDir := filepath.Join(dir, rig, "mayor", "rig", "linkshelf/internal/store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store_test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []PlanBead{{ID: "te-3xo", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	})
	out := "internal/store/store_test.go:113:5: undefined: timestampsApproximatelyEqual\nFAIL\tlinkshelf/internal/store [build failed]\n"
	got := FormatForeignOpenBeadTestCompileHint(dir, rig, "linkshelf/internal/store/schema.go", out, v)
	if !strings.Contains(got, "store_test.go") || !strings.Contains(got, "te-3xo") {
		t.Fatalf("hint = %q", got)
	}
}
