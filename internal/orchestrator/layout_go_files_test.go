package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoCompileVerifyCommandForBead_frontendReturnsEmpty(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/web/style.css",
			"linkshelf/web/app.js",
		},
	}
	dir := t.TempDir()
	for _, path := range []string{
		"linkshelf/web/index.html",
		"linkshelf/web/style.css",
		"linkshelf/web/app.js",
	} {
		got := GoCompileVerifyCommandForBead(v, dir, path)
		if got != "" {
			t.Errorf("frontend path %q should return empty verify cmd, got %q", path, got)
		}
	}
}

func TestGoCompileVerifyCommandForBead_storePackage(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	dir := t.TempDir()
	got := GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/store.go")
	if got != "cd linkshelf && go mod tidy && go build ./internal/store/..." {
		t.Fatalf("production bead before test file exists: got %q", got)
	}
	testFile := filepath.Join(dir, "linkshelf/internal/store/store_test.go")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got = GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/store.go")
	if got != "cd linkshelf && go mod tidy && go test -timeout 30s -count=1 ./internal/store/..." {
		t.Fatalf("production bead after test file exists: got %q", got)
	}
}

func TestGoCompileVerifyCommandForBead_foreignTestWithoutOwnTest(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "linkshelf/internal/store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store\nfunc TestStore(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/schema.go")
	if got != "cd linkshelf && go mod tidy && go build ./internal/store/..." {
		t.Fatalf("schema bead with foreign store_test.go on disk: got %q", got)
	}
}

func TestPruneStaleLayoutFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "store.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "sqlite.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
		},
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, _ string) ([]PlanBead, error) {
		return nil, nil
	})
	removed, err := PruneStaleLayoutFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "linkshelf/internal/store/sqlite.go" {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(filepath.Join(layout, "sqlite.go")); !os.IsNotExist(err) {
		t.Fatal("sqlite.go should be gone")
	}
}

func TestPruneStaleLayoutFiles_keepsCorrelatedTest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"store.go":      "package store\n",
		"store_test.go": "package store\n",
		"extra.go":      "package store\n",
	} {
		if err := os.WriteFile(filepath.Join(layout, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:   []string{"linkshelf/internal/store/store.go"},
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, _ string) ([]PlanBead, error) {
		return nil, nil
	})
	removed, err := PruneStaleLayoutFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "linkshelf/internal/store/extra.go" {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(filepath.Join(layout, "store_test.go")); err != nil {
		t.Fatalf("store_test.go should remain: %v", err)
	}
}

func TestPruneStaleLayoutFiles_removesFlatDuplicateWhenNestedRequired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "linkshelf")
	apiDir := filepath.Join(layout, "internal", "api")
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"handlers.go":                    "package api\n",
		"internal/api/handlers.go":       "package api\n",
		"internal/api/handlers_test.go":  "package api\n",
	} {
		full := filepath.Join(layout, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
			"linkshelf/internal/api/handlers_test.go",
		},
	}
	removed, err := PruneStaleLayoutFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "linkshelf/handlers.go" {
		t.Fatalf("flat handlers.go should be pruned, removed = %v", removed)
	}
	if _, err := os.Stat(filepath.Join(layout, "handlers.go")); !os.IsNotExist(err) {
		t.Fatal("flat handlers.go should be gone")
	}
}

func TestPruneStaleLayoutFiles_keepsFlatMainWhenBeadsUseCmdPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "pingapp")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"main.go":      "package main\n",
		"main_test.go": "package main\n",
	} {
		if err := os.WriteFile(filepath.Join(layout, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		RequiredFiles: []string{
			"pingapp/main.go",
			"pingapp/main_test.go",
		},
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, _ string) ([]PlanBead, error) {
		return nil, nil
	})
	removed, err := PruneStaleLayoutFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("flat main.go should survive when beads use flat paths, removed = %v", removed)
	}
}

func TestPruneStaleLayoutFiles_skipsWhenImplementBeadsActive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "sqlite.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
		},
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "te-1", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	})
	removed, err := PruneStaleLayoutFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "linkshelf/internal/store/sqlite.go" {
		t.Fatalf("expected unlisted sqlite.go pruned while store bead active, removed = %v", removed)
	}
}

func TestLayoutRelPathsProtectedFromPrune_includesCorrelatedTest(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:   []string{"linkshelf/internal/store/schema.go"},
	}
	got := layoutRelPathsProtectedFromPrune(v)
	if !got["internal/store/schema.go"] || !got["internal/store/schema_test.go"] {
		t.Fatalf("protected = %v", got)
	}
}

func TestPruneStaleLayoutFiles_removesStalePyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "myapp", "backend")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "main.py"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "stale_old.py"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot: "myapp",
		RequiredFiles: []string{
			"myapp/backend/main.py",
		},
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, _ string) ([]PlanBead, error) {
		return nil, nil
	})
	removed, err := PruneStaleLayoutFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "myapp/backend/stale_old.py" {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(filepath.Join(layout, "stale_old.py")); !os.IsNotExist(err) {
		t.Fatal("stale_old.py should be gone")
	}
	if _, err := os.Stat(filepath.Join(layout, "main.py")); err != nil {
		t.Fatal("main.py should remain")
	}
}

func TestPruneStaleLayoutFiles_keepsNonManagedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "myapp")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	// .md files are not in managedSourceExtensions
	if err := os.WriteFile(filepath.Join(layout, "README.md"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	// .txt files are not managed
	if err := os.WriteFile(filepath.Join(layout, "notes.txt"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	// .gitkeep is not managed
	if err := os.WriteFile(filepath.Join(layout, ".gitkeep"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot: "myapp",
		RequiredFiles: []string{
			"myapp/main.py",
		},
	}
	removed, err := PruneStaleLayoutFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("expected no removals for non-managed files, removed = %v", removed)
	}
}
