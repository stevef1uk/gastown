package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoToolchainMismatch(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("exit status 1")
	out := `compile: version "go1.25.8" does not match go tool version "go1.25.6"`
	if !GoToolchainMismatch(err, out) {
		t.Fatal("expected mismatch")
	}
}

func TestGoTestArgsFromVerify(t *testing.T) {
	t.Parallel()
	v := DefaultWorkflowValidation()
	v.QAVerifyCommand = "cd linkshelf && go test -count=1 ./internal/api/..."
	got := goTestArgsFromVerify(v)
	want := []string{"test", "-count=1", "./internal/api/..."}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func requireGoToolchain(t *testing.T) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH")
	}
	if mismatch := detectGoToolchainMismatch(goBin); mismatch != "" {
		t.Skip("go toolchain mismatch on host (align `go version` with `go tool compile -V`): " + mismatch)
	}
}

// detectGoToolchainMismatch returns a short message when go version and compile tool disagree.
func detectGoToolchainMismatch(goBin string) string {
	verOut, err := exec.Command(goBin, "version").CombinedOutput()
	if err != nil {
		return ""
	}
	toolOut, err := exec.Command(goBin, "tool", "compile", "-V").CombinedOutput()
	if err != nil {
		return ""
	}
	ver := goVersionTag(string(verOut))
	tool := goVersionTag(string(toolOut))
	if ver == "" || tool == "" || ver == tool {
		return ""
	}
	return fmt.Sprintf("go version reports %s but go tool compile reports %s", ver, tool)
}

func goVersionTag(s string) string {
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "go1.") {
			return tok
		}
	}
	return ""
}

func TestImplementationModuleCompileOK_passesCleanModule(t *testing.T) {
	requireGoToolchain(t)
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(filepath.Join(layout, "internal", "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "internal", "pkg", "pkg.go"), []byte("package pkg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	if err := ImplementationModuleCompileOK(dir, v); err != nil {
		if strings.Contains(err.Error(), "command not found") || strings.Contains(err.Error(), "not in PATH") {
			t.Skip("go not available in test shell")
		}
		if GoToolchainMismatch(err, err.Error()) {
			t.Skip("go toolchain mismatch on host (reinstall Go so `go version` matches GOROOT tools): " + err.Error())
		}
		t.Fatal(err)
	}
}

func TestImplementationModuleCompileOK_failsBrokenModule(t *testing.T) {
	requireGoToolchain(t)
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(filepath.Join(layout, "internal", "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "internal", "pkg", "pkg.go"), []byte("package pkg\n\nfoo bar\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	err := ImplementationModuleCompileOK(dir, v)
	if err == nil {
		t.Fatal("expected compile failure")
	}
	if strings.Contains(err.Error(), "command not found") || strings.Contains(err.Error(), "not in PATH") {
		t.Skip("go not available in test shell")
	}
	if GoToolchainMismatch(err, err.Error()) {
		t.Skip("go toolchain mismatch on host: " + err.Error())
	}
}
