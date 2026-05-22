package orchestrator

import "testing"

func TestInjectSQLiteSchemaBead_addsSchemaBeforeStore(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		TestRunner: "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	got := InjectSQLiteSchemaBead(v)
	schema := SQLiteSchemaBeadPath("linkshelf")
	found := false
	for _, f := range got.RequiredFiles {
		if f == schema {
			found = true
		}
	}
	if !found {
		t.Fatalf("required_files missing %q: %v", schema, got.RequiredFiles)
	}
	order := OrderRequiredFilesForImplementation(got.RequiredFiles)
	if len(order) < 2 || order[0] != "linkshelf/go.mod" {
		t.Fatalf("order: %v", order)
	}
	if order[1] != schema {
		t.Fatalf("schema should follow go.mod, got order: %v", order)
	}
}

func TestInjectSQLiteSchemaBead_idempotent(t *testing.T) {
	t.Parallel()
	schema := "linkshelf/internal/store/schema.go"
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			schema,
			"linkshelf/internal/store/store.go",
		},
	}
	got := InjectSQLiteSchemaBead(v)
	if len(got.RequiredFiles) != len(v.RequiredFiles) {
		t.Fatalf("got %v want %v", got.RequiredFiles, v.RequiredFiles)
	}
}

func TestInjectSQLiteSchemaBead_deliveryPhase(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		DeliveryPhases: []DeliveryPhase{{
			ID: "backend",
			RequiredFiles: []string{
				"linkshelf/go.mod",
				"linkshelf/internal/store/store.go",
			},
		}},
	}
	got := InjectSQLiteSchemaBead(v)
	schema := SQLiteSchemaBeadPath("linkshelf")
	phase := got.DeliveryPhases[0].RequiredFiles
	if !containsStr(phase, schema) {
		t.Fatalf("phase files missing schema: %v", phase)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestInjectSQLiteSchemaBead_skipsWithoutStorePackage(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "myapp",
		TestRunner:      "go",
		QAVerifyCommand: "cd myapp && go test ./...",
		RequiredFiles: []string{
			"myapp/go.mod",
			"myapp/internal/api/handlers.go",
		},
	}
	got := InjectSQLiteSchemaBead(v)
	schema := SQLiteSchemaBeadPath("myapp")
	for _, f := range got.RequiredFiles {
		if f == schema {
			t.Fatalf("should not inject schema without internal/store: %v", got.RequiredFiles)
		}
	}
}

func TestInjectSQLiteSchemaBead_skipsNonGoProfile(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "myapp",
		TestRunner: "pytest",
		RequiredFiles: []string{
			"myapp/internal/store/store.py",
		},
	}
	got := InjectSQLiteSchemaBead(v)
	if len(got.RequiredFiles) != 1 {
		t.Fatalf("got %v", got.RequiredFiles)
	}
}

func TestClampProfileValidation_injectsSchemaForLinkshelfProfile(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		TestRunner: "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		SpecSummary: "SQLite storage bookmark manager",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/store.go",
		},
		DeliveryPhases: []DeliveryPhase{{
			ID:            "backend",
			RequiredFiles: []string{"linkshelf/go.mod", "linkshelf/internal/store/store.go"},
		}},
	}
	got := ClampProfileValidation(v)
	schema := SQLiteSchemaBeadPath("linkshelf")
	if !containsStr(got.RequiredFiles, schema) {
		t.Fatalf("union required_files: %v", got.RequiredFiles)
	}
	if !containsStr(got.DeliveryPhases[0].RequiredFiles, schema) {
		t.Fatalf("phase required_files: %v", got.DeliveryPhases[0].RequiredFiles)
	}
}
