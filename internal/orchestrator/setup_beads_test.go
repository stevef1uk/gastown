package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsProjectSetupArtifactPath(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{"tasklist/requirements.txt", "tasklist/tasklist/store.py"},
		QAVerifyCommand: "pytest -v",
	}
	if !IsProjectSetupArtifactPath("tasklist/requirements.txt", v) {
		t.Fatal("requirements.txt is setup-owned")
	}
	if IsProjectSetupArtifactPath("tasklist/tasklist/store.py", v) {
		t.Fatal("store.py is polecat-owned")
	}
	goV := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		RequiredFiles:   []string{"linkshelf/go.mod", "linkshelf/cmd/server/main.go"},
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	if !IsProjectSetupArtifactPath("linkshelf/go.mod", goV) {
		t.Fatal("go.mod is setup-owned")
	}
}

func TestNextOpenImplementBead_skipsSetupArtifacts(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	tasklist := filepath.Join(mayor, "tasklist", "tasklist")
	if err := os.MkdirAll(tasklist, 0755); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "tasklist",
		RequiredFiles:   []string{"tasklist/requirements.txt", "tasklist/tasklist/__init__.py"},
		BeadTitleContains: "Implement ",
	}
	// Simulate beads without bd: call NextOpenImplementBead only checks open beads from bd.
	// Test ordering skip via IsProjectSetupArtifactPath on order head.
	order := OrderRequiredFilesForImplementation(v.RequiredFiles)
	var polecatOrder []string
	for _, p := range order {
		if !IsProjectSetupArtifactPath(p, v) {
			polecatOrder = append(polecatOrder, p)
		}
	}
	if len(polecatOrder) == 0 || polecatOrder[0] != "tasklist/tasklist/__init__.py" {
		t.Fatalf("polecat queue should skip requirements first: %v", polecatOrder)
	}
}

func TestProjectSetupArtifactReady_python(t *testing.T) {
	workDir := t.TempDir()
	reqPath := filepath.Join(workDir, "tasklist", "requirements.txt")
	if err := os.MkdirAll(filepath.Dir(reqPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reqPath, []byte("pytest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	venv := filepath.Join(workDir, ".venv")
	if out, err := exec.Command("python3", "-m", "venv", venv).CombinedOutput(); err != nil {
		t.Skipf("venv: %v %s", err, out)
	}
	venvPy := filepath.Join(venv, "bin", "python3")
	if out, err := exec.Command(venvPy, "-m", "pip", "install", "pytest").CombinedOutput(); err != nil {
		t.Skipf("pip install pytest: %v %s", err, out)
	}
	v := WorkflowValidation{
		RequiredFiles: []string{"tasklist/requirements.txt"},
		QAVerifyCommand: "pytest -v",
	}
	if !projectSetupArtifactReady(workDir, "tasklist/requirements.txt", v) {
		t.Fatal("expected ready when requirements exist and venv imports pytest")
	}
}
