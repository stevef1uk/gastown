package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentShellVerifyCommand_rewritesLayoutCD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testgt3", "mayor", "rig", "linkshelf")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	storeDir := filepath.Join(rigDir, "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store\nfunc TestX(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mayorDir := filepath.Join(dir, "testgt3", "mayor", "rig")
	got := AgentShellVerifyCommand("testgt3", v, mayorDir, "linkshelf/internal/store/schema.go")
	if !strings.Contains(got, "cd testgt3/mayor/rig/linkshelf &&") {
		t.Fatalf("want town-root cd, got %q", got)
	}
	if !strings.Contains(got, "go build ./internal/store/...") {
		t.Fatalf("want go build for foreign store_test.go, got %q", got)
	}
	if strings.Contains(got, "go test") {
		t.Fatalf("must not suggest go test: %q", got)
	}
}

func TestRewriteVerifyCDForWorkPath(t *testing.T) {
	t.Parallel()
	in := "cd linkshelf && go mod tidy && go build ./internal/store/..."
	got := RewriteVerifyCDForWorkPath(in, "linkshelf", "testgt3/mayor/rig/linkshelf")
	want := "cd testgt3/mayor/rig/linkshelf && go mod tidy && go build ./internal/store/..."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNormalizeGoLayoutPackagePathsRemovesDuplicateGoCD(t *testing.T) {
	for _, flag := range []string{"pingapp", "./pingapp", "\"pingapp\"", "'pingapp'"} {
		input := "cd pwtest/mayor/rig/pingapp && go -C " + flag + " test ./... && go -C " + flag + " build ./cmd/server"
		want := "cd pwtest/mayor/rig/pingapp && go test ./... && go build ./cmd/server"
		if got := NormalizeGoLayoutPackagePaths(input, "pwtest/mayor/rig/pingapp", "pingapp"); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
	input := "go -C other test ./..."
	if got := NormalizeGoLayoutPackagePaths(input, "pwtest/mayor/rig/pingapp", "pingapp"); got != input {
		t.Fatal("changed a different module's -C flag")
	}
}

func TestNormalizeGoLayoutPackagePathsRemovesBareLayoutPaths(t *testing.T) {
	input := "cd pwtest/mayor/rig/pingapp && go test ./pingapp/...; go run pingapp/cmd/server/main.go"
	want := "cd pwtest/mayor/rig/pingapp && go test ./...; go run ./cmd/server/main.go"
	if got := NormalizeGoLayoutPackagePaths(input, "pwtest/mayor/rig/pingapp", "pingapp"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
