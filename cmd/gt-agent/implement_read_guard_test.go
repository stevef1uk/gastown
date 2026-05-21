package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateImplementationMissingFileRead_rejectsCatMissingTest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "linkshelf",
		TestRunner: "go",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	cmd := "cat linkshelf/internal/store/store_test.go"
	err := validateImplementationMissingFileRead(cmd, dir, rig, "te-store", "linkshelf/internal/store/store.go", v)
	if err == nil {
		t.Fatal("expected reject cat on missing separate test file")
	}
	if !strings.Contains(err.Error(), "separate implement bead") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateImplementationMissingFileRead_allowsSpec(t *testing.T) {
	t.Parallel()
	err := validateImplementationMissingFileRead("cat SPEC.md", t.TempDir(), "mockrig", "", "", orchestrator.WorkflowValidation{})
	if err != nil {
		t.Fatalf("spec read should be allowed: %v", err)
	}
}
