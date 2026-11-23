package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsurePythonRuntimeDepsLog_addsPinnedServerDeps(t *testing.T) {
	townRoot := t.TempDir()
	rig := "demo"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	writeRigFile(t, rigDir, "taskboard/requirements.txt", "pytest\n")
	writeRigFile(t, rigDir, "taskboard/main.py", "from fastapi import FastAPI\nfrom fastapi.testclient import TestClient\n")
	writeRigFile(t, rigDir, "Dockerfile", "FROM python:3.12-slim\nRUN pip install --no-cache-dir fastapi==0.115.0 uvicorn==0.30.6\n")

	v := WorkflowValidation{
		LayoutRoot:      "taskboard",
		RequiredFiles:   []string{"taskboard/requirements.txt", "taskboard/main.py"},
		QAVerifyCommand: "cd taskboard && python -m pytest -q tests",
		TestRunner:      "pytest",
		DevServerPort:   8080,
		DeliveryPhases: []DeliveryPhase{
			{ID: "app", RequiredFiles: []string{"taskboard/main.py"}, QAVerifyCommand: "cd taskboard && python -m pytest -q tests"},
		},
	}
	log, err := EnsurePythonRuntimeDepsLog(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "taskboard/requirements.txt") {
		t.Fatalf("unexpected log: %q", log)
	}
	data, err := os.ReadFile(filepath.Join(rigDir, "taskboard", "requirements.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fastapi==0.115.0", "uvicorn==0.30.6", "httpx"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("requirements missing %q: %s", want, data)
		}
	}
	if strings.Count(string(data), "pytest") != 1 {
		t.Fatalf("pytest duplicated: %s", data)
	}
	log, err = EnsurePythonRuntimeDepsLog(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if log != "" {
		t.Fatalf("second call should be a no-op, got %q", log)
	}
}

func TestEnsurePythonRuntimeDepsLog_skipsLibraryOnlyWorkflow(t *testing.T) {
	townRoot := t.TempDir()
	rig := "lib"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	req := "tasklib/requirements.txt"
	writeRigFile(t, rigDir, req, "pytest\n")
	v := WorkflowValidation{
		LayoutRoot:      "tasklib",
		RequiredFiles:   []string{req, "tasklib/lib.py"},
		QAVerifyCommand: "cd tasklib && python -m pytest -q",
		TestRunner:      "pytest",
	}
	log, err := EnsurePythonRuntimeDepsLog(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if log != "" {
		t.Fatalf("library-only workflow changed: %q", log)
	}
	data, err := os.ReadFile(filepath.Join(rigDir, req))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "pytest" {
		t.Fatalf("requirements changed: %s", data)
	}
}

func TestEnsurePythonRequirementsFile(t *testing.T) {
	files := []string{"app/pyproject.toml", "app/main.py"}
	got := ensurePythonRequirementsFile(files, "app")
	want := []string{"app/pyproject.toml", "app/main.py", "app/requirements.txt"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if again := ensurePythonRequirementsFile(got, "app"); len(again) != len(want) {
		t.Fatalf("duplicate requirements path: %v", again)
	}
}

func TestRequirementsFilePathPrefersRequirements(t *testing.T) {
	v := WorkflowValidation{RequiredFiles: []string{"app/pyproject.toml", "app/requirements.txt"}}
	if got := v.RequirementsFilePath(); got != "app/requirements.txt" {
		t.Fatalf("got %q", got)
	}
}
