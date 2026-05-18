package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if err := os.WriteFile(storePath, []byte("package store\nimport \"linkshelf/internal/model\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/internal/store/store.go"},
		SpecSummary:       "The store in linkshelf/internal/store/store.go uses SQLite for links.",
		LayoutRoot:        "linkshelf",
	}
	got := formatImplementBeadContextForPath(dir, rig, "linkshelf/internal/store/store.go", v)

	for _, want := range []string{
		"## Implement context for `linkshelf/internal/store/store.go`",
		"do not invent packages",
		"### From architecture.md",
		"List, Create, Delete",
		"Do not add separate model packages",
		"### From workflow profile",
		"SQLite for links",
		"### Current file on disk",
		"linkshelf/internal/model",
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
		"### From workflow profile",
		"### Current file on disk",
		"package store // stale",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
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