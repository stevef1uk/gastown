package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoCompileOutputHasUnusedImport(t *testing.T) {
	t.Parallel()
	out := `# linkshelf/internal/api [linkshelf/internal/api.test]
./handlers_test.go:6:2: "fmt" imported and not used
FAIL`
	if !GoCompileOutputHasUnusedImport(out) {
		t.Fatal("expected true")
	}
	if GoCompileOutputHasUnusedImport("syntax error at line 1") {
		t.Fatal("expected false")
	}
}

func TestFormatUnusedImportCompileHint(t *testing.T) {
	t.Parallel()
	h := FormatUnusedImportCompileHint(`"fmt" imported and not used`)
	if h == "" || !strings.Contains(h, "small EDIT") {
		t.Fatalf("hint = %q", h)
	}
}

func TestGoFilePathsFromCompileOutput(t *testing.T) {
	t.Parallel()
	out := `# linkshelf/internal/api [linkshelf/internal/api.test]
./handlers_test.go:6:2: "fmt" imported and not used
FAIL`
	got := GoFilePathsFromCompileOutput(out, "linkshelf")
	if len(got) != 1 || got[0] != "linkshelf/internal/api/handlers_test.go" {
		t.Fatalf("paths = %v", got)
	}
}

func TestGoCompilePackageDirFromOutput(t *testing.T) {
	t.Parallel()
	out := `# linkshelf/internal/api [linkshelf/internal/api.test]`
	got := goCompilePackageDirFromOutput(out, "linkshelf")
	if got != "linkshelf/internal/api" {
		t.Fatalf("pkg dir = %q", got)
	}
}

func TestRunGoimportsOnCompileOutput_package(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	pkg := filepath.Join(layout, "internal", "api")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testBody := "package api\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) {}\n"
	prodBody := "package api\n\nimport \"fmt\"\n\nfunc F() {}\n"
	if err := os.WriteFile(filepath.Join(pkg, "handlers_test.go"), []byte(testBody), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "handlers.go"), []byte(prodBody), 0644); err != nil {
		t.Fatal(err)
	}
	out := `# linkshelf/internal/api [linkshelf/internal/api.test]
./handlers_test.go:6:2: "fmt" imported and not used`
	touched, ran, err := RunGoimportsOnCompileOutput(dir, "linkshelf", out)
	if err != nil {
		if !ran {
			t.Skip("goimports not installed")
		}
		t.Fatal(err)
	}
	if !ran {
		t.Skip("goimports not installed")
	}
	if len(touched) < 1 {
		t.Fatalf("expected touched files, got %v", touched)
	}
	data, err := os.ReadFile(filepath.Join(pkg, "handlers_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"fmt"`) {
		t.Fatalf("expected fmt removed from test file:\n%s", data)
	}
}

func TestRunGoimportsOnFile_removesUnusedImport(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	pkg := filepath.Join(layout, "internal", "api")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	body := "package api\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) {}\n"
	path := filepath.Join(pkg, "handlers_test.go")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	ran, err := RunGoimportsOnFile(dir, "linkshelf/internal/api/handlers_test.go")
	if err != nil {
		if !ran {
			t.Skip("goimports not installed")
		}
		t.Fatal(err)
	}
	if !ran {
		t.Skip("goimports not installed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"fmt"`) {
		t.Fatalf("expected fmt removed:\n%s", data)
	}
}
