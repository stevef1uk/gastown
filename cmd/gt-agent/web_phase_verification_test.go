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

func TestImplementationBeadRequiresVerify(t *testing.T) {
	requires := []string{
		"app/backend/app/main.py",
		"app/backend/app/db/seed.py",
		"app/cmd/server/main.go",
		"app/internal/api/handlers_test.go",
	}
	notRequired := []string{
		"app/planning/PLAN.md",
		"app/Dockerfile",
		"app/docker-compose.yml",
		"app/frontend/package.json",
		"app/frontend/tsconfig.json",
		"app/scripts/start_mac.sh",
		"app/scripts/stop_windows.ps1",
		"app/.gitignore",
		"app/db/.gitkeep",
		"app/frontend/src/app/page.tsx",
		"app/frontend/src/app/globals.css",
	}
	for _, p := range requires {
		if !orchestrator.ImplementationBeadRequiresVerify(p) {
			t.Errorf("%s should require a real verify", p)
		}
	}
	for _, p := range notRequired {
		if orchestrator.ImplementationBeadRequiresVerify(p) {
			t.Errorf("%s should close on disk validation, not require a phase verify", p)
		}
	}
}

// TestNonSourceBeadClosesBeforeLaterSourceBead reproduces the phase-01 deadlock:
// a doc/config bead whose phase build cannot go green until a later package.json
// bead exists must still be closeable once its artifact validates on disk.
func TestNonSourceBeadClosesBeforeLaterSourceBead(t *testing.T) {
	dir := t.TempDir()
	plan := "app/planning/PLAN.md"
	if err := os.MkdirAll(filepath.Join(dir, "app/planning"), 0755); err != nil {
		t.Fatal(err)
	}
	body := "# Phase 01\n\n- goal one\n- goal two\n- goal three\n"
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(plan)), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:                 "app",
		BeadTitleContains:          "Implement app/",
		MinImplementationFileBytes: 10,
		MinSubstantiveLines:        2,
		QAVerifyCommand:            "docker compose -f app/docker-compose.yml build",
		RequiredFiles:              []string{plan, "app/frontend/package.json"},
	}
	if !implementationBeadHasVerification(dir, plan, v, false) {
		t.Fatal("non-source artifact blocked on a later source/package.json bead")
	}
	if implementationBeadHasVerification(dir, "app/backend/app/main.py", v, false) {
		t.Fatal("backend source verify requirement weakened")
	}
	if implementationBeadHasVerification(dir, "app/planning/MISSING.md", v, false) {
		t.Fatal("missing non-source artifact passed")
	}
}

// TestPythonNonSourceBeadClosesWithoutPhaseVerify covers the dominant phase-01
// deadlock: on a Python rig the per-bead close gate must not require the phase
// build for a doc/config bead whose artifact already validates on disk.
func TestPythonNonSourceBeadClosesWithoutPhaseVerify(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	mayor := rigMayorRigDir(town, rig)
	plan := "app/planning/PLAN.md"
	if err := os.MkdirAll(filepath.Join(mayor, "app", "planning"), 0755); err != nil {
		t.Fatal(err)
	}
	body := "# Phase 01\n\n- goal one\n- goal two\n- goal three\n"
	if err := os.WriteFile(filepath.Join(mayor, filepath.FromSlash(plan)), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:                 "app",
		BeadTitleContains:          "Implement ",
		TestRunner:                 "pytest",
		MinImplementationFileBytes: 10,
		MinSubstantiveLines:        2,
		QAVerifyCommand:            "docker compose -f app/docker-compose.yml build",
		RequiredFiles:              []string{plan, "app/frontend/package.json"},
	}
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "sm-plan", Title: "Implement app/planning/PLAN.md per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	close := "cd sample/mayor/rig && bd close sm-plan"
	if err := validateImplementationCommandWithState(close, town, rig, "sm-plan", v, false, nil, ""); err != nil {
		t.Fatalf("python non-source bead should close on disk validation: %v", err)
	}
}

// TestPythonFrontendBeadClosesOnDiskValidation covers the resumed-session
// deadlock: a frontend bead whose artifact already validates on disk never
// triggers a fresh write (so verifyOK stays false), and the close gate must
// still allow bd close instead of demanding an in-session EDIT forever.
func TestPythonFrontendBeadClosesOnDiskValidation(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	mayor := rigMayorRigDir(town, rig)
	front := "app/frontend/next.config.js"
	if err := os.MkdirAll(filepath.Join(mayor, "app", "frontend"), 0755); err != nil {
		t.Fatal(err)
	}
	body := "/** @type {import('next').NextConfig} */\nconst nextConfig = {\n  output: \"export\",\n  trailingSlash: true,\n};\nmodule.exports = nextConfig;\n"
	if err := os.WriteFile(filepath.Join(mayor, filepath.FromSlash(front)), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:                 "app",
		BeadTitleContains:          "Implement ",
		TestRunner:                 "pytest",
		MinImplementationFileBytes: 10,
		MinSubstantiveLines:        2,
		QAVerifyCommand:            "docker compose -f app/docker-compose.yml build",
		RequiredFiles:              []string{front, "app/frontend/package.json"},
	}
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "sm-front", Title: "Implement app/frontend/next.config.js per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	close := "cd sample/mayor/rig && bd close sm-front"
	if err := validateImplementationCommandWithState(close, town, rig, "sm-front", v, false, nil, ""); err != nil {
		t.Fatalf("python frontend bead should close on disk validation: %v", err)
	}
}

func TestRewriteNpmPrefixDoesNotRewriteDocumentHeredoc(t *testing.T) {
	cmd := "cat > plan.md <<'EOF'\nVerify: npm --prefix app run build\nEOF"
	v := orchestrator.WorkflowValidation{LayoutRoot: "app", TestRunner: "go"}
	if fixed, changed := rewriteUnittestToWorkdir(cmd, "demo", v); changed || fixed != cmd {
		t.Fatalf("document payload rewritten: %s", fixed)
	}
}
