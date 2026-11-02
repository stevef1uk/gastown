package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatCodingStandardsBlock_GoExcludesPython(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement ",
		QAVerifyCommand:   "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/store.go",
			"linkshelf/cmd/server/main.go",
		},
	}

	got := PromptContextBlock("coding_standards", t.TempDir(), "myrig", v)
	if !strings.Contains(got, "Town Hall Coding Standards (Go)") {
		t.Fatalf("expected Go coding standards, got:\n%s", got)
	}
	if strings.Contains(got, "Python") || strings.Contains(got, "pytest") || strings.Contains(got, "FastAPI") {
		t.Fatalf("Go project must not include Python coding standards:\n%s", got)
	}
	if len(got) > maxCodingStandardsContextBytes {
		t.Fatalf("coding standards block %d bytes exceeds cap %d", len(got), maxCodingStandardsContextBytes)
	}
}

func TestFormatCodingStandardsBlock_PythonExcludesGo(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "defender/backend",
		BeadTitleContains: "Implement ",
		QAVerifyCommand:   "python3 -m pytest -q defender/backend",
		TestRunner:        "pytest",
		RequiredFiles: []string{
			"defender/backend/main.py",
			"defender/backend/requirements.txt",
			"defender/backend/tests/test_api.py",
		},
	}

	got := PromptContextBlock("coding_standards", t.TempDir(), "defender", v)
	if !strings.Contains(got, "Town Hall Coding Standards (Python)") {
		t.Fatalf("expected Python coding standards, got:\n%s", got)
	}
	if strings.Contains(got, "defender/backend") == false {
		t.Fatalf("expected {{layout_root}} substitution in Python standards, got:\n%s", got)
	}
	if strings.Contains(got, "(Go)") || strings.Contains(got, "package main") || strings.Contains(got, "httptest") {
		t.Fatalf("Python project must not include Go coding standards:\n%s", got)
	}
}

func TestFormatCodingStandardsBlock_ActiveBeadNarrowsMultiStack(t *testing.T) {
	dir := t.TempDir()
	rig := "defender"
	v := WorkflowValidation{
		LayoutRoot:        "defender",
		BeadTitleContains: "Implement ",
		QAVerifyCommand:   "python3 -m pytest -q defender/backend",
		RequiredFiles: []string{
			"defender/backend/main.py",
			"defender/frontend/game/main.js",
			"defender/schema.sql",
			"defender/Dockerfile",
		},
	}

	prev := nextOpenImplementBeadHook
	t.Cleanup(func() { nextOpenImplementBeadHook = prev })

	// 1. Active bead is JS frontend -> only TypeScript/JS standards
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return &PlanBead{ID: "de-1", Title: "Implement defender/frontend/game/main.js"}, nil
	}
	jsGot := FormatCodingStandardsBlock(dir, rig, v)
	if !strings.Contains(jsGot, "Town Hall Coding Standards (TypeScript / JavaScript / Web)") {
		t.Fatalf("expected TypeScript/JS standards for .js bead, got:\n%s", jsGot)
	}
	if strings.Contains(jsGot, "(Python)") || strings.Contains(jsGot, "(Go)") {
		t.Fatalf("expected only JS standards for .js bead, got:\n%s", jsGot)
	}

	// 2. Active bead is SQL -> only SQL standards
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return &PlanBead{ID: "de-2", Title: "Implement defender/schema.sql"}, nil
	}
	sqlGot := FormatCodingStandardsBlock(dir, rig, v)
	if !strings.Contains(sqlGot, "Town Hall Coding Standards (SQL)") {
		t.Fatalf("expected SQL standards for .sql bead, got:\n%s", sqlGot)
	}

	// 3. Active bead is Dockerfile -> only Docker standards
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return &PlanBead{ID: "de-3", Title: "Implement defender/Dockerfile"}, nil
	}
	dockerGot := FormatCodingStandardsBlock(dir, rig, v)
	if !strings.Contains(dockerGot, "Town Hall Coding Standards (Docker & Compose)") {
		t.Fatalf("expected Docker standards for Dockerfile bead, got:\n%s", dockerGot)
	}
}

func TestSyncTownAssets_installsTechnologyStandards(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := SyncTownAssets(dir, true); err != nil {
		t.Fatal(err)
	}
	for _, stack := range []string{"go", "python", "typescript", "sql", "docker"} {
		p := filepath.Join(dir, TownAssetsSubdir, "standards", stack+".md")
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected synced standard file %s: %v", p, err)
		}
	}
}
