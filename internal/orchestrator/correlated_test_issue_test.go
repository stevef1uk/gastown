package orchestrator

import (
	"testing"
)

func TestCorrelatedTestPathsCreateExtraBeads(t *testing.T) {
	t.Parallel()
	
	v := WorkflowValidation{
		LayoutRoot:        "hello_api",
		BeadTitleContains: "Implement hello_api/",
		RequiredFiles: []string{
			"hello_api/go.mod",
			"hello_api/handler.go",
			"hello_api/handler_test.go",
			"hello_api/main.go",
			"hello_api/README.md",
		},
		DeliveryPhases: nil,
	}
	
	// This is what requiredFilesWithCorrelatedTests returns
	augmented := requiredFilesWithCorrelatedTests(v.RequiredFiles, v)
	t.Logf("Original required_files: %v", v.RequiredFiles)
	t.Logf("Augmented: %v", augmented)
	
	// The problem: handler_test.go and main_test.go are added but they don't exist in required_files
	// This causes extra beads to be created
	
	// Check if main_test.go is incorrectly added
	foundMainTest := false
	for _, f := range augmented {
		if f == "hello_api/main_test.go" {
			foundMainTest = true
			break
		}
	}
	
	if foundMainTest {
		t.Errorf("main_test.go was incorrectly added to augmented list - it doesn't exist in required_files")
	}
	
	// Check handler_test.go - this IS in required_files so it's correct
	foundHandlerTest := false
	for _, f := range augmented {
		if f == "hello_api/handler_test.go" {
			foundHandlerTest = true
			break
		}
	}
	if !foundHandlerTest {
		t.Errorf("handler_test.go should be in augmented list")
	}
}
