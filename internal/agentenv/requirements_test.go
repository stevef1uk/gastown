package agentenv

import (
	"os"
	"strings"
	"testing"
)

func TestSanitizeRequirementsText(t *testing.T) {
	in := "python3 -m pytest\n/home/u/.venv/bin/python3 -m pytest\npytest>=7.0\n# comment\n\n"
	got := SanitizeRequirementsText(in)
	if !strings.Contains(got, "pytest>=7.0") || !strings.Contains(got, "# comment") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "python3 -m") || strings.Contains(got, "/python3") {
		t.Fatalf("shell lines remain: %q", got)
	}
}

func TestRequirementsPathFromPipInstall(t *testing.T) {
	cmd := `cd rig && python3 -m pip install -r "defender/backend/requirements.txt"`
	if got := RequirementsPathFromPipInstall(cmd); got != "defender/backend/requirements.txt" {
		t.Fatalf("got %q", got)
	}
}

func TestRepairRequirementsFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/requirements.txt"
	if err := os.WriteFile(path, []byte("python3 -m pytest\npytest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := RepairRequirementsFile(path)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "python3") {
		t.Fatalf("got %q", data)
	}
	if !strings.Contains(string(data), "pytest") {
		t.Fatalf("got %q", data)
	}
}
