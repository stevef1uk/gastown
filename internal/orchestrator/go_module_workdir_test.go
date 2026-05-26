package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoModuleCDDir_flatModuleAtMayorRig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module linkshelf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "store"), 0755); err != nil {
		t.Fatal(err)
	}
	got := GoModuleCDDir(dir, "linkshelf")
	if got != "." {
		t.Fatalf("GoModuleCDDir = %q, want . for flat module", got)
	}
}

func TestGoModuleCDDir_nestedLayoutDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := GoModuleCDDir(dir, "linkshelf")
	if got != "linkshelf" {
		t.Fatalf("GoModuleCDDir = %q, want linkshelf", got)
	}
}

func TestGoCompileVerifyCommandForBead_flatLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module linkshelf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(dir, "internal", "store")
	if err := os.MkdirAll(store, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "schema.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "schema_test.go"), []byte("package store\nfunc TestInitSchema(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go"},
	}
	cmd := GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/schema.go")
	if want := "go mod tidy && go test -count=1 ./internal/store/..."; cmd != want {
		t.Fatalf("verify cmd = %q, want %q", cmd, want)
	}
}

func TestResolveImplementRelPathOnDisk_flatLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "internal", "store", "schema.go")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	got := ResolveImplementRelPathOnDisk(dir, "linkshelf/internal/store/schema.go", "linkshelf")
	if got != "internal/store/schema.go" {
		t.Fatalf("got %q want internal/store/schema.go", got)
	}
}
