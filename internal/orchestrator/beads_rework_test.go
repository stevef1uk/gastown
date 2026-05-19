package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBeadImplementationNeedsRework_missingFile(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	v := WorkflowValidation{MinImplementationFileBytes: 10}
	if !beadImplementationNeedsRework(rigDir, "tasklist/foo.py", v) {
		t.Fatal("missing file should need rework")
	}
}

func TestBeadImplementationNeedsRework_substantiveFile(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	path := filepath.Join(rigDir, "tasklist", "store.py")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	body := "def load():\n    return {}\n\ndef save(s):\n    pass\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{MinImplementationFileBytes: 10, MinSubstantiveLines: 2}
	if beadImplementationNeedsRework(rigDir, "tasklist/store.py", v) {
		t.Fatal("substantive file should not need rework")
	}
}

func TestImplementationDiskWorkReady_allPresent(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	req := filepath.Join(rigDir, "tasklist", "requirements.txt")
	if err := os.MkdirAll(filepath.Dir(req), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(req, []byte("pytest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(rigDir, "tasklist", "store.py")
	body := "def load():\n    return {}\n\ndef save(s):\n    pass\n"
	if err := os.WriteFile(store, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		RequiredFiles:             []string{"tasklist/requirements.txt", "tasklist/store.py"},
		QAVerifyCommand:           "pytest -v",
		MinImplementationFileBytes: 10,
		MinSubstantiveLines:        2,
	}
	if err := ImplementationDiskWorkReady(rigDir, v); err != nil {
		t.Fatalf("ready: %v", err)
	}
}

func TestReopenClosedImplementBeads_selective(t *testing.T) {
	t.Parallel()
	// beadImplementationNeedsRework false for good file → reopen loop skips (tested via needsRework helpers).
	rigDir := t.TempDir()
	store := filepath.Join(rigDir, "tasklist", "store.py")
	if err := os.MkdirAll(filepath.Dir(store), 0755); err != nil {
		t.Fatal(err)
	}
	body := "def load():\n    return {}\n\ndef save(s):\n    pass\n"
	if err := os.WriteFile(store, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{MinImplementationFileBytes: 10, MinSubstantiveLines: 2}
	if beadImplementationNeedsRework(rigDir, "tasklist/store.py", v) {
		t.Fatal("should not reopen bead with good store.py")
	}
	if !beadImplementationNeedsRework(rigDir, "tasklist/__main__.py", v) {
		t.Fatal("missing __main__.py should need rework")
	}
}
