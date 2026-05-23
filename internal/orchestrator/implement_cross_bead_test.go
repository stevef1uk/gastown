package orchestrator

import (
	"strings"
	"testing"
)

func TestValidateImplementCrossBeadContent_rejectsInitSchemaInStore(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
		},
	}
	body := "package store\n\nfunc InitSchema(db *sql.DB) error { return nil }\n"
	err := ValidateImplementCrossBeadContent("linkshelf/internal/store/store.go", body, v)
	if err == nil || !strings.Contains(err.Error(), "InitSchema") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateImplementCrossBeadContent_allowsInitSchemaOnSchemaBead(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:   []string{"linkshelf/internal/store/schema.go"},
	}
	body := "package store\n\nfunc InitSchema(db *sql.DB) error { return nil }\n"
	if err := ValidateImplementCrossBeadContent("linkshelf/internal/store/schema.go", body, v); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestValidateImplementCrossBeadContent_rejectsStoreMethodsOnSchema(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:   []string{"linkshelf/internal/store/schema.go"},
	}
	body := "package store\n\nfunc NewStore(path string) (*Store, error) { return nil, nil }\n"
	err := ValidateImplementCrossBeadContent("linkshelf/internal/store/schema.go", body, v)
	if err == nil || !strings.Contains(err.Error(), "NewStore") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateImplementWrittenContent_crossBead(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
		},
	}
	err := ValidateImplementWrittenContent("linkshelf/internal/store/store.go", "func InitSchema() {}\n", v)
	if err == nil {
		t.Fatal("expected error")
	}
}
