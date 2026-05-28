package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatImplementBeadContextForPath_includesAcceptanceChecklist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "internal", "store"), 0755); err != nil {
		t.Fatal(err)
	}
	plan := `### te-1: linkshelf/internal/store/store.go
- Acceptance: AddLink persists rows
`
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		TestRunner:    "go",
		RequiredFiles: []string{"linkshelf/internal/store/store.go"},
	}
	got := formatImplementBeadContextForPath(dir, rig, "linkshelf/internal/store/store.go", v)
	if !strings.Contains(got, "### Acceptance checklist") || !strings.Contains(got, "AddLink persists rows") {
		t.Fatalf("missing checklist in:\n%s", got)
	}
}

func TestFormatImplementBeadContextForPath_fullBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	storePath := filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")
	if err := os.MkdirAll(filepath.Dir(storePath), 0755); err != nil {
		t.Fatal(err)
	}
	arch := `## Data layer
` + "`linkshelf/internal/store/store.go`" + ` provides List, Create, Delete using modernc.org/sqlite.
Do not add separate model packages.
`
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/internal/store/store.go"},
		SpecSummary:       "The store in linkshelf/internal/store/store.go uses SQLite for links.",
		LayoutRoot:        "linkshelf",
	}

	substantiveBody := "package store\nimport \"linkshelf/internal/model\"\n\n" + strings.Repeat("func (s *Store) GetAll() ([]Link, error) { return nil, nil }\n", 100)
	if err := os.WriteFile(storePath, []byte(substantiveBody), 0644); err != nil {
		t.Fatal(err)
	}
	got := formatImplementBeadContextForPath(dir, rig, "linkshelf/internal/store/store.go", v)

	for _, want := range []string{
		"## Implement context for `linkshelf/internal/store/store.go`",
		"do not invent packages",
		"### From architecture.md",
		"List, Create, Delete",
		"Do not add separate model packages",
		"### Current file on disk",
		"linkshelf/internal/model",
		"### Incremental edit required",
		"sed -i",
		"Do not** use `cat >",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "app.js") {
		t.Fatalf("should not pull unrelated architecture paths: %q", got)
	}
}

func TestFormatImplementBeadContextBlock_linkshelfTitlePrefix(t *testing.T) {
	dir := t.TempDir()
	rig := "testgt3"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	storePath := filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")
	if err := os.MkdirAll(filepath.Dir(storePath), 0755); err != nil {
		t.Fatal(err)
	}
	arch := "`linkshelf/internal/store/store.go` provides List, Create, Delete.\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storePath, []byte("package store // stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/internal/store/store.go"},
		SpecSummary:       "Data access lives in linkshelf/internal/store/store.go with SQLite.",
		LayoutRoot:        "linkshelf",
	}
	prev := nextOpenImplementBeadHook
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return &PlanBead{
			ID:    "te-0em",
			Title: "Implement linkshelf/internal/store/store.go per architecture",
		}, nil
	}
	t.Cleanup(func() { nextOpenImplementBeadHook = prev })

	got := FormatImplementBeadContextBlock(dir, rig, v)
	for _, want := range []string{
		"## Implement context for `linkshelf/internal/store/store.go`",
		"### From architecture.md",
		"List, Create, Delete",
		"### Current file on disk",
		"package store // stale",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatImplementBeadContextForPath_includesDependencyStore(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	storePath := filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")
	mainPath := filepath.Join(rigDir, "linkshelf", "cmd", "server", "main.go")
	if err := os.MkdirAll(filepath.Dir(storePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mainPath), 0755); err != nil {
		t.Fatal(err)
	}
	handlersPath := filepath.Join(rigDir, "linkshelf", "internal", "api", "handlers.go")
	if err := os.MkdirAll(filepath.Dir(handlersPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handlersPath, []byte("package api\n\nfunc registerHandlers(mux *http.ServeMux) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	storeBody := "package store\n\nfunc List() {}\n"
	if err := os.WriteFile(storePath, []byte(storeBody), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	got := formatImplementBeadContextForPath(dir, rig, "linkshelf/cmd/server/main.go", v)
	for _, want := range []string{
		"### Main wiring",
		"registerAPI",
		"### Dependency packages",
		"handlers.go",
		"cmd/main bead",
		"do not re-implement handler",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "GetAllLinks") {
		t.Fatalf("stale GetAllLinks should not appear in main context:\n%s", got)
	}
	if strings.Contains(got, "### Incremental edit required") {
		t.Fatal("cmd/main should not show incremental-edit block")
	}
}

func TestFormatIncrementalEditBlock_direct(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	layout := "linkshelf"
	rel := layout + "/internal/store/store.go"
	abs := filepath.Join(dir, rig, "mayor", "rig", filepath.Dir(rel))
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "package main\n\n" + strings.Repeat("func main() { println(1) }\n", 200)
	if err := os.WriteFile(filepath.Join(dir, rig, "mayor", "rig", rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: layout}
	got := FormatIncrementalEditBlock(dir, rig, rel, v)
	for _, want := range []string{"Incremental edit required", "sed -i", "patch"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatIncrementalEditBlock_omittedForStub(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rel := "linkshelf/internal/new.go"
	if err := os.MkdirAll(filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rig, "mayor", "rig", rel), []byte("TODO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	if got := FormatIncrementalEditBlock(dir, rig, rel, v); got != "" {
		t.Fatalf("stub should not require incremental block: %q", got)
	}
}

func TestFormatImplementBeadContextBlock_emptyWithoutBead(t *testing.T) {
	prev := nextOpenImplementBeadHook
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return nil, nil
	}
	t.Cleanup(func() { nextOpenImplementBeadHook = prev })

	v := WorkflowValidation{RequiredFiles: []string{"linkshelf/go.mod"}}
	if got := FormatImplementBeadContextBlock(t.TempDir(), "rig", v); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestPromptContextBlock_implementBeadContext_includesCodeindexEarly(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	layout := filepath.Join(rigDir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "schema.go"), []byte("package store\nfunc InitSchema() error { return nil }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte("`linkshelf/internal/api/handlers.go` routes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	apiDir := filepath.Join(rigDir, "linkshelf", "internal", "api")
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "handlers.go"), []byte("package api\n\nfunc RegisterHandlers() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		LayoutRoot:        "linkshelf",
		QAVerifyCommand:   "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/api/handlers.go",
		},
	}
	if !CodeindexEnabled() {
		t.Skip("codeindex not on PATH")
	}
	if _, err := RefreshCodeindexIndex(rigDir, v); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	prev := nextOpenImplementBeadHook
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return &PlanBead{ID: "te-api", Title: "Implement linkshelf/internal/api/handlers.go per architecture"}, nil
	}
	t.Cleanup(func() { nextOpenImplementBeadHook = prev })

	got := PromptContextBlock("implement_bead_context", dir, rig, v)
	if !strings.Contains(got, "### Codeindex symbols") {
		t.Fatalf("want codeindex block, got:\n%s", got)
	}
	archIdx := strings.Index(got, "### From architecture.md")
	codeIdx := strings.Index(got, "### Codeindex symbols")
	if archIdx >= 0 && codeIdx > archIdx {
		t.Fatalf("codeindex should appear before architecture excerpt")
	}
}

func TestPromptContextBlock_implementBeadContext(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte("`linkshelf/go.mod` module root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"linkshelf/go.mod"},
	}
	prev := nextOpenImplementBeadHook
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return &PlanBead{ID: "te-1", Title: "Implement linkshelf/go.mod per architecture"}, nil
	}
	t.Cleanup(func() { nextOpenImplementBeadHook = prev })

	got := PromptContextBlock("implement_bead_context", dir, rig, v)
	if got == "" {
		t.Fatal("expected non-empty implement_bead_context block")
	}
	if unknown := PromptContextBlock("no_such_hook", dir, rig, v); unknown != "" {
		t.Fatalf("unknown hook should be empty, got %q", unknown)
	}
}

func TestPromptContextBlocks_implementationHooks(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte("`linkshelf/go.mod` module\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"linkshelf/go.mod"},
	}
	prev := nextOpenImplementBeadHook
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return &PlanBead{ID: "te-1", Title: "Implement linkshelf/go.mod per architecture"}, nil
	}
	t.Cleanup(func() { nextOpenImplementBeadHook = prev })

	keys := []string{"implementation_queue", "implement_bead_context"}
	blocks := PromptContextBlocks(keys, dir, rig, v)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: %v", len(blocks), blocks)
	}
	if !strings.Contains(blocks[0], "**Next bead") {
		t.Fatalf("first block should be queue: %q", blocks[0])
	}
	if !strings.Contains(blocks[1], "## Implement context") {
		t.Fatalf("second block should be bead context: %q", blocks[1])
	}
}

func TestRigFlowYAML_implementationHasImplementBeadContext(t *testing.T) {
	tpl := loadRigFlowTemplate(t)
	h := tpl.States["implementation"].Hooks
	want := []string{"implementation_queue", "implement_bead_context"}
	if len(h.PromptContext) != len(want) {
		t.Fatalf("prompt_context = %v, want %v", h.PromptContext, want)
	}
	for i, key := range want {
		if h.PromptContext[i] != key {
			t.Fatalf("prompt_context[%d] = %q, want %q", i, h.PromptContext[i], key)
		}
	}
	if len(h.FailurePromptContext) != len(want) {
		t.Fatalf("failure_prompt_context = %v, want %v", h.FailurePromptContext, want)
	}
	for i, key := range want {
		if h.FailurePromptContext[i] != key {
			t.Fatalf("failure_prompt_context[%d] = %q, want %q", i, h.FailurePromptContext[i], key)
		}
	}
}

func TestExcerptLinesForPath_skipsUnrelated(t *testing.T) {
	t.Parallel()
	doc := "alpha\n`linkshelf/web/app.js` for UI\n`linkshelf/internal/store/store.go` does List\n"
	got := excerptLinesForPath(doc, "linkshelf/internal/store/store.go", "linkshelf", 500)
	if strings.Contains(got, "app.js") {
		t.Fatalf("should not include unrelated path: %q", got)
	}
	if !strings.Contains(got, "List") {
		t.Fatalf("want store line: %q", got)
	}
}

func TestFormatImplementBeadContextForPath_includesHTTPRoutingGuidance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeHTTPContractFixture(t, dir, rig, "<html></html>", "package api\n")
	v := linkshelfHTTPProfile()
	got := formatImplementBeadContextForPath(dir, rig, "linkshelf/internal/api/handlers_test.go", v)
	for _, want := range []string{"HTTP routing", "os.Chdir", "web/"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestSpecSummaryExcerptForBead_longParagraph(t *testing.T) {
	t.Parallel()
	path := "linkshelf/internal/store/store.go"
	long := "The " + path + " file uses sqlite. " + strings.Repeat("padding ", 200)
	got := specSummaryExcerptForBead(long, path, "linkshelf")
	if !strings.Contains(got, "sqlite") {
		t.Fatalf("want path-related excerpt: %q", got)
	}
	if !strings.Contains(got, path) {
		t.Fatalf("want path in excerpt: %q", got)
	}
}

func TestFormatGoModuleImportContext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf"), 0755); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf"}

	// No go.mod
	if got := formatGoModuleImportContext(rigDir, v); got != "" {
		t.Errorf("expected empty string when go.mod is missing, got %q", got)
	}

	// Write go.mod
	goModContent := "module github.com/user/linkshelf\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(rigDir, "linkshelf", "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	got := formatGoModuleImportContext(rigDir, v)
	if !strings.Contains(got, "The Go module name for this project is `github.com/user/linkshelf`") {
		t.Errorf("missing expected module name in output: %q", got)
	}
}