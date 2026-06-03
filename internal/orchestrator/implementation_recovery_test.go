package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunOnTimeoutHook_recoverImplementationStall_unknown(t *testing.T) {
	t.Parallel()
	_, err := RunOnTimeoutHook("recover_implementation_stall_typo", "/tmp", "rig", DefaultWorkflowValidation())
	if err == nil || !strings.Contains(err.Error(), "unknown on_timeout") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecoverImplementationStall_noBeads(t *testing.T) {
	t.Parallel()
	logLine, err := RecoverImplementationStall(t.TempDir(), "norigr", DefaultWorkflowValidation())
	if err != nil {
		t.Fatal(err)
	}
	if logLine != "" {
		t.Fatalf("expected no work without beads DB, got %q", logLine)
	}
}

func TestResetInProgressImplementBeads_integration(t *testing.T) {
	if os.Getenv("BD_TEST") == "" {
		t.Skip("set BD_TEST=1 to run bd integration test")
	}
	townRoot := os.Getenv("GT_TEST_TOWN")
	if townRoot == "" {
		t.Skip("GT_TEST_TOWN required")
	}
	rig := os.Getenv("GT_TEST_RIG")
	if rig == "" {
		rig = "testgt3"
	}
	v := DefaultWorkflowValidation()
	reset, err := ResetInProgressImplementBeads(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	_ = reset
}

func TestRemoveLayoutSourceCodeFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	layout := filepath.Join(dir, "applayout")
	storeDir := filepath.Join(layout, "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	storeGo := filepath.Join(storeDir, "store.go")
	storeTest := filepath.Join(storeDir, "store_test.go")
	py := filepath.Join(layout, "pkg", "worker.py")
	if err := os.MkdirAll(filepath.Dir(py), 0755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		storeGo:    "package store\n",
		storeTest:  "package store\n",
		py:         "x = 1\n",
		filepath.Join(layout, "go.mod"):        "module applayout\n\ngo 1.22\n",
		filepath.Join(layout, "go.sum"):         "applayout v0.0.0\n",
		filepath.Join(layout, "requirements.txt"): "pytest\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "applayout"
	removed, err := RemoveLayoutSourceCodeFiles(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed=%v want 3", removed)
	}
	for _, keep := range []string{filepath.Join(layout, "go.mod"), filepath.Join(layout, "go.sum"), filepath.Join(layout, "requirements.txt")} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("should keep %s: %v", keep, err)
		}
	}
	for _, gone := range []string{storeGo, storeTest, py} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("should remove %s", gone)
		}
	}
}

func TestRunOnTimeoutHook_resetImplementationPhase_unknownTypo(t *testing.T) {
	t.Parallel()
	_, err := RunOnTimeoutHook("reset_implementation_phase_typo", "/tmp", "rig", DefaultWorkflowValidation())
	if err == nil || !strings.Contains(err.Error(), "unknown on_timeout") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveImplementBeadArtifactFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	layout := filepath.Join(dir, "app")
	web := filepath.Join(layout, "web")
	if err := os.MkdirAll(web, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"app/internal/handler.go": "package x\n",
		"app/web/index.html":      "<html></html>\n",
		"app/go.mod":              "module app\n",
	}
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := RemoveImplementBeadArtifactFiles(dir, []string{"app/internal/handler.go", "app/web/index.html", "app/go.mod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed=%v want 2 (go.mod kept)", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "app/go.mod")); err != nil {
		t.Fatal("go.mod should remain")
	}
}

func TestImplementArtifactPathsForActiveBeads(t *testing.T) {
	t.Parallel()
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.BeadTitleContains = "Implement"
	v.RequiredFiles = []string{
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/api/handlers.go",
		"linkshelf/web/index.html",
	}
	prev := ListImplementBeadsByStatusHook
	defer func() { ListImplementBeadsByStatusHook = prev }()
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "open":
			return []PlanBead{{ID: "te-open", Title: "Implement linkshelf/web/index.html per architecture"}}, nil
		case "in_progress":
			return []PlanBead{{ID: "te-ip", Title: "Implement linkshelf/internal/api/handlers.go per architecture"}}, nil
		case "closed":
			return []PlanBead{{ID: "te-cl", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		default:
			return nil, nil
		}
	}
	paths, err := implementArtifactPathsForActiveBeads("/gt", "rig", v)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"linkshelf/web/index.html":         true,
		"linkshelf/internal/api/handlers.go": true,
	}
	if len(paths) != len(want) {
		t.Fatalf("paths=%v want %v", paths, want)
	}
	for _, p := range paths {
		if !want[p] {
			t.Fatalf("unexpected path %q", p)
		}
	}
}

func TestClearImplementationProgressFile(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "r"
	path := filepath.Join(town, rig, "qa", "implementation-progress.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	ok, err := ClearImplementationProgressFile(town, rig)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected removed")
	}
}

func TestStopRigDevServersScriptPath(t *testing.T) {
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scripts, "stop-rig-dev-servers.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GASTOWN", dir)
	got := stopRigDevServersScriptPath()
	if got != path {
		t.Fatalf("got %q want %q", got, path)
	}
}
