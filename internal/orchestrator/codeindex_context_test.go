package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodeindexIndexNeedsRefresh_missingIndex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if !codeindexIndexNeedsRefresh(filepath.Join(dir, "codeindex.json"), dir) {
		t.Fatal("want refresh when index missing")
	}
}

func TestCodeindexIndexNeedsRefresh_staleIndex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	goFile := filepath.Join(src, "a.go")
	if err := os.WriteFile(goFile, []byte("package pkg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, "codeindex.json")
	if err := os.WriteFile(index, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(index, past, past); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(goFile, future, future); err != nil {
		t.Fatal(err)
	}
	if !codeindexIndexNeedsRefresh(index, dir) {
		t.Fatal("want refresh when sources newer than index")
	}
}

func TestFormatCodeindexContextForBead_disabled(t *testing.T) {
	t.Setenv("GT_CODEINDEX", "0")
	got := FormatCodeindexContextForBead(t.TempDir(), "linkshelf/foo.go", WorkflowValidation{LayoutRoot: "linkshelf"})
	if got != "" {
		t.Fatalf("want empty when disabled, got %q", got)
	}
}

func TestCodeindexImpactCandidates_goFile(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	got := codeindexImpactCandidates("linkshelf/internal/store/schema.go", v)
	if len(got) != 2 || got[0] != "internal/store" || got[1] != "internal/store/schema.go" {
		t.Fatalf("got %v", got)
	}
}

func TestCodeindexLayoutRelativePath(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	got := codeindexLayoutRelativePath("linkshelf/internal/api/handlers.go", v)
	if got != "internal/api/handlers.go" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatCodeindexSymbolsSection(t *testing.T) {
	t.Parallel()
	got := formatCodeindexSymbolsSection("dependency internal/store", "internal/store", "func InitSchema()\nfunc NewStore()", 500)
	for _, want := range []string{
		"### Codeindex symbols (dependency internal/store)",
		"do not invent",
		"func InitSchema()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCodeindexDependencySymbolPaths_handlersBead(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/api/handlers.go",
		},
	}
	got := codeindexDependencySymbolPaths("linkshelf/internal/api/handlers.go", v)
	if len(got) == 0 {
		t.Fatal("want dependency packages")
	}
	if got[0] != "internal/store" {
		t.Fatalf("got %v", got)
	}
}

func TestFormatCodeindexContextForBead_goPackageImpact(t *testing.T) {
	if !CodeindexEnabled() {
		t.Skip("codeindex not on PATH")
	}
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	body := "package store\n\nfunc InitSchema() error { return nil }\n"
	if err := os.WriteFile(filepath.Join(layout, "schema.go"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	if _, err := RefreshCodeindexIndex(dir, v); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got := FormatCodeindexContextForBead(dir, "linkshelf/internal/store/schema.go", v)
	if got == "" {
		t.Fatal("want blast radius block")
	}
	if strings.Contains(got, "impact lookup failed") || strings.Contains(got, "File not found in index") {
		t.Fatalf("file path should fall back to package impact, got:\n%s", got)
	}
	if !strings.Contains(got, "Codeindex blast radius") && !strings.Contains(got, "Codeindex symbols") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "InitSchema") {
		t.Fatalf("want auto-injected symbols section, got:\n%s", got)
	}
	if !strings.Contains(got, "codeindex symbols linkshelf/internal/store --index codeindex.json") {
		t.Fatalf("want symbols CMD hint, got:\n%s", got)
	}
}

func TestCodeindexPolecatCMDExamples_goPackage(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	got := CodeindexPolecatCMDExamples("linkshelf/internal/api/handlers.go", v)
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if got[0] != "codeindex symbols linkshelf/internal/api --index codeindex.json" {
		t.Fatalf("symbols cmd = %q", got[0])
	}
	if got[1] != "codeindex impact internal/api --index codeindex.json" {
		t.Fatalf("impact cmd = %q", got[1])
	}
}

func TestExtractInlineSymbolsFromCodeindex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	index := filepath.Join(dir, "codeindex.json")
	payload := `{
		"nodes": [{
			"id": "internal/store",
			"symbols": [
				{"name": "InitSchema", "kind": "function", "exported": true, "file": "schema.go", "line": 7},
				{"name": "helper", "kind": "function", "exported": false, "file": "schema.go", "line": 20}
			]
		}]
	}`
	if err := os.WriteFile(index, []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}
	got := extractInlineSymbolsFromCodeindex(index, []string{"internal/store"})
	if !strings.Contains(got, "InitSchema") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "helper") {
		t.Fatalf("unexported symbol should be omitted, got %q", got)
	}
}

func TestAppendCodeindexPolecatCMDHint_includesSymbolsImpactAndRefresh(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	appendCodeindexPolecatCMDHint(&b, "linkshelf/internal/api/handlers.go", v)
	got := b.String()
	for _, want := range []string{
		"Optional CMD refresh",
		"symbols are already injected above",
		"CMD: codeindex symbols linkshelf/internal/api --index codeindex.json",
		"exported funcs/types in scope",
		"CMD: codeindex impact internal/api --index codeindex.json",
		"who imports this path/package",
		"CMD: codeindex analyze linkshelf --output codeindex.json && codeindex symbols linkshelf --inline --index codeindex.json",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in hint block:\n%s", want, got)
		}
	}
}

func TestFormatCodeindexContextForBead_polecatHintWhenImpactMissing(t *testing.T) {
	if !CodeindexEnabled() {
		t.Skip("codeindex not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codeindex.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	got := FormatCodeindexContextForBead(dir, "linkshelf/internal/api/handlers.go", v)
	if got == "" {
		t.Fatal("want context block when index exists")
	}
	if !strings.Contains(got, "codeindex symbols linkshelf/internal/api --index codeindex.json") {
		t.Fatalf("want symbols CMD hint even without impact output, got:\n%s", got)
	}
	if !strings.Contains(got, "Refresh stale index") {
		t.Fatalf("want refresh CMD hint, got:\n%s", got)
	}
}

func TestTruncateCodeindexText(t *testing.T) {
	t.Parallel()
	got := truncateCodeindexText("abcdef", 4)
	if got != "abcd\n…" {
		t.Fatalf("got %q", got)
	}
}
