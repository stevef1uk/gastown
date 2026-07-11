package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestStateRunner_backfillCodeCache_cachesExistingDockerfile(t *testing.T) {
	townRoot := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}

	arch := "# Architecture\n\n## Docker & Deployment\n\n```dockerfile\nFROM node:20-slim\nFROM python:3.12-slim\nEXPOSE 8000\nCMD [\"uvicorn\", \"backend.main:app\", \"--host\", \"0.0.0.0\", \"--port\", \"8000\"]\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}

	dockerfile := "FROM node:20-slim\nFROM python:3.12-slim\nEXPOSE 8000\nCMD [\"uvicorn\", \"backend.main:app\", \"--host\", \"0.0.0.0\", \"--port\", \"8000\"]\n"
	if err := os.WriteFile(filepath.Join(rigDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		t.Fatal(err)
	}

	task := &orchestrator.Task{
		WorkflowID: "wf-backfill",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation"},
		Validation: orchestrator.WorkflowValidation{
			LayoutRoot:      ".",
			TestRunner:      "custom",
			QAVerifyCommand: "docker build .",
			RequiredFiles:   []string{"Dockerfile"},
		},
	}

	r := newStateRunner(task, townRoot, rig)
	r.backfillCodeCache()

	cache, err := orchestrator.OpenCodeCache(rigDir, task.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	content, ok := cache.GetValidated(r.v.ActivePhaseIndex(), "Dockerfile")
	if !ok {
		t.Fatal("expected Dockerfile to be backfilled into cache")
	}
	if content != dockerfile {
		t.Fatalf("cached content mismatch:\n%s", content)
	}
	_ = r
}
