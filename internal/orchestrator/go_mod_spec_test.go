package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateGoModFile_requiresSpecDirectives(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(`# Spec

## Module

`+"```"+`
module linkshelf

go 1.22

require github.com/mattn/go-sqlite3 v1.14.22
`+"```"+`
`), 0644); err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", QAVerifyCommand: "cd linkshelf && go test ./..."}
	if err := ValidateGoModFile(dir, v); err == nil {
		t.Fatal("expected missing sqlite require")
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte(`module linkshelf

go 1.22

require github.com/mattn/go-sqlite3 v1.14.22
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGoModFile(dir, v); err != nil {
		t.Fatalf("valid go.mod: %v", err)
	}
}

func TestValidateGoModFile_blockRequireSyntax(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(`## Module

`+"```"+`
module linkshelf

go 1.22

require github.com/mattn/go-sqlite3 v1.14.22
`+"```"+`
`), 0644); err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	mod := `module linkshelf

go 1.22

require (
	github.com/mattn/go-sqlite3 v1.14.22
)
`
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte(mod), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", QAVerifyCommand: "cd linkshelf && go test ./..."}
	if err := ValidateGoModFile(dir, v); err != nil {
		t.Fatalf("block require: %v", err)
	}
}

func TestRepairGoModRequiresFromSpec_afterTidyStripsRequire(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(`## Module

`+"```"+`
module linkshelf

go 1.22

require github.com/mattn/go-sqlite3 v1.14.22
`+"```"+`
`), 0644); err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	// go mod tidy on empty module removes unused requires
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", QAVerifyCommand: "cd linkshelf && go test ./..."}
	logLine, err := RepairGoModRequiresFromSpec(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	if logLine == "" {
		t.Fatal("expected repair log")
	}
	if err := ValidateGoModFile(dir, v); err != nil {
		t.Fatalf("after repair: %v", err)
	}
}

func TestEnsureGoModFromSpec_closesOpenBeadWhenValid(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "## Module\n\n```\nmodule linkshelf\n\ngo 1.22\n\nrequire github.com/mattn/go-sqlite3 v1.14.22\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(rigDir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n\nrequire github.com/mattn/go-sqlite3 v1.14.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:         "linkshelf",
		ActivePhaseIDField: "go-module",
		QAVerifyCommand:    "cd linkshelf && go test ./...",
		RequiredFiles:      []string{"linkshelf/go.mod"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}},
		},
	}
	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	t.Cleanup(func() { bdCloseImplementBeadHook = nil })
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []PlanBead{{ID: "te-mod", Title: "Implement linkshelf/go.mod per architecture"}}, nil
		}
		return nil, nil
	})

	logLine, err := EnsureGoModFromSpec(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if logLine == "" || !strings.Contains(logLine, "auto-closed") {
		t.Fatalf("logLine = %q", logLine)
	}
	if len(closed) != 1 || closed[0] != "te-mod" {
		t.Fatalf("closed = %v", closed)
	}
}

func TestEnsureGoModFromSpec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "## Module\n\n```\nmodule linkshelf\n\ngo 1.22\n\nrequire github.com/mattn/go-sqlite3 v1.14.22\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(rigDir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		ActivePhaseIDField: "go-module",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:   []string{"linkshelf/go.mod"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}},
		},
	}
	logLine, err := EnsureGoModFromSpec(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if logLine == "" {
		t.Fatal("expected patch log")
	}
	if err := ValidateGoModFile(rigDir, v.ForActivePhase()); err != nil {
		t.Fatalf("after patch: %v", err)
	}
}

func TestRequiredGoModRequireDirectives(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	spec := "require github.com/mattn/go-sqlite3 v1.14.22\n"
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("```\nmodule linkshelf\n\n"+spec+"```"), 0644); err != nil {
		t.Fatal(err)
	}
	got := RequiredGoModRequireDirectives(dir)
	if len(got) != 1 || got[0] != "require github.com/mattn/go-sqlite3 v1.14.22" {
		t.Fatalf("got %v", got)
	}
}
