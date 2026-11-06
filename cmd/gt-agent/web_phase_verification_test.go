package main

import (
	"github.com/steveyegge/gastown/internal/orchestrator"
	"os"
	"path/filepath"
	"testing"
)

func TestRewriteNpmPrefixKeepsRepositoryWorkdir(t *testing.T) {
	v := orchestrator.WorkflowValidation{LayoutRoot: "app", TestRunner: "go", RequiredFiles: []string{"app/go.mod", "app/package.json", "app/web/app.js"}}
	suffix := "npm --prefix app install && npm --prefix app run build && test -f app/web/app.js"
	want := "cd demo/mayor/rig && " + suffix
	for _, cmd := range []string{suffix, "cd demo/mayor/rig && " + suffix, "cd demo/mayor/rig/app && " + suffix, "cd demo/mayor/rig && cd app && " + suffix} {
		fixed, _ := rewriteUnittestToWorkdir(cmd, "demo", v)
		if fixed != want {
			t.Errorf("%q => %q; want %q", cmd, fixed, want)
		}
	}
}

func TestFrontendBeadCanCloseBeforeLaterBuildManifest(t *testing.T) {
	dir := t.TempDir()
	path := "app/web/app.js"
	if err := os.MkdirAll(filepath.Join(dir, "app/web"), 0755); err != nil {
		t.Fatal(err)
	}
	body := "const button = document.querySelector('button');\nbutton.addEventListener('click', () => {\n  document.querySelector('#message').textContent = 'Clicked';\n});\n"
	if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{LayoutRoot: "app", QAVerifyCommand: "npm --prefix app run build", RequiredFiles: []string{path, "app/package.json"}, MinImplementationFileBytes: 20, MinSubstantiveLines: 2}
	if !implementationBeadHasVerification(dir, path, v, false) {
		t.Fatal("valid frontend artifact blocked on a later manifest bead")
	}
	if implementationBeadHasVerification(dir, "app/web/missing.js", v, false) {
		t.Fatal("missing frontend artifact passed")
	}
	if implementationBeadHasVerification(dir, "app/cmd/server/main.go", v, false) {
		t.Fatal("backend verify requirement weakened")
	}
	r := &stateRunner{v: v, track: &cmdTracker{}}
	if err := artifactAutoCompleters["implementation"](r); err == nil {
		t.Fatal("whole-phase verification bypassed")
	}
}

func TestRewriteNpmPrefixDoesNotRewriteDocumentHeredoc(t *testing.T) {
	cmd := "cat > plan.md <<'EOF'\nVerify: npm --prefix app run build\nEOF"
	v := orchestrator.WorkflowValidation{LayoutRoot: "app", TestRunner: "go"}
	if fixed, changed := rewriteUnittestToWorkdir(cmd, "demo", v); changed || fixed != cmd {
		t.Fatalf("document payload rewritten: %s", fixed)
	}
}
