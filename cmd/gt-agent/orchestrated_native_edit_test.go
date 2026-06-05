package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func stubImplementBeadsHook(t *testing.T, beads ...orchestrator.PlanBead) {
	t.Helper()
	prev := orchestrator.ListImplementBeadsByStatusHook
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "open" || status == "in_progress" || status == "closed" {
			return beads, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = prev })
}

func linkshelfImplementValidation(files ...string) orchestrator.WorkflowValidation {
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.BeadTitleContains = "Implement "
	v.RequiredFiles = files
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	return v
}

func writeLinkshelfStoreTree(t *testing.T, mayor, storeBody string) {
	t.Helper()
	if storeBody == "" {
		storeBody = "package store\n"
	}
	files := map[string]string{
		"linkshelf/go.mod":                  "module linkshelf\n\ngo 1.22\n",
		"linkshelf/internal/store/store.go": storeBody,
	}
	for rel, body := range files {
		p := filepath.Join(mayor, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func closedHandlersBeadHook() {
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		switch status {
		case "closed":
			return []orchestrator.PlanBead{{
				ID:    "te-h",
				Title: "Implement linkshelf/internal/api/handlers.go per architecture",
			}}, nil
		case "in_progress":
			return []orchestrator.PlanBead{{
				ID:    "te-main",
				Title: "Implement linkshelf/cmd/server/main.go per architecture",
			}}, nil
		default:
			return nil, nil
		}
	}
}

func TestParseNativeWriteBody_splitTerminator(t *testing.T) {
	t.Parallel()
	lines := []string{
		"package store",
		"---",
		"END WRITE---",
	}
	body, next := parseNativeWriteBody(lines, 0)
	if strings.Contains(body, "END WRITE") || strings.Contains(body, "---") {
		t.Fatalf("body should be clean and not contain split terminator: %q", body)
	}
	if body != "package store" {
		t.Fatalf("unexpected body: %q", body)
	}
	if next != 3 {
		t.Fatalf("next=%d want 3", next)
	}
}

func TestParseNativeWriteBody_stopsAtNextToolOrFence(t *testing.T) {
	t.Parallel()
	lines := []string{
		"package store",
		"}",
		"```",
		"WRITE: linkshelf/internal/store/schema_test.go",
		"package store_test",
		"---END WRITE---",
	}
	body, next := parseNativeWriteBody(lines, 0)
	if strings.Contains(body, "WRITE:") || strings.Contains(body, "schema_test") {
		t.Fatalf("first write swallowed next tool: %q", body)
	}
	if next != 3 {
		t.Fatalf("next=%d want 3 (line after closing ```)", next)
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[next]), "WRITE:") {
		t.Fatalf("line %d = %q, want WRITE:", next, lines[next])
	}
	body2, next2 := parseNativeWriteBody(lines, next+1)
	if !strings.Contains(body2, "package store_test") {
		t.Fatalf("second write: %q", body2)
	}
	if next2 != 6 {
		t.Fatalf("next2=%d want 6", next2)
	}
}

func TestParseOrchestratedNativeEdits_multiWriteWithoutEndMarker(t *testing.T) {
	in := "WRITE: linkshelf/internal/store/schema.go\npackage store\n\nfunc InitSchema() {}\n```\n" +
		"WRITE: linkshelf/internal/store/schema_test.go\npackage store_test\n" +
		"CMD: cd linkshelf && go test ./internal/store/...\n"
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 2 {
		t.Fatalf("ops=%d want 2: %+v", len(ops), ops)
	}
	if ops[0].kind != "write" || !strings.Contains(ops[0].path, "schema.go") {
		t.Fatalf("first: %+v", ops[0])
	}
	if strings.Contains(ops[0].content, "CMD:") {
		t.Fatalf("first write swallowed CMD: %q", ops[0].content)
	}
	if ops[1].kind != "write" || !strings.Contains(ops[1].path, "schema_test.go") {
		t.Fatalf("second: %+v", ops[1])
	}
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "go test") {
		t.Fatalf("cmds=%v", cmds)
	}
}

func TestParseOrchestratedNativeEdits_editAndWrite(t *testing.T) {
	in := `EDIT: linkshelf/internal/store/store.go
<<<<<<< SEARCH
func Old() {}
=======
func New() {}
>>>>>>> REPLACE

WRITE: linkshelf/internal/new/x.go
package x
---END WRITE---
`
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 2 {
		t.Fatalf("ops=%d want 2", len(ops))
	}
	if ops[0].kind != "edit" || !strings.Contains(ops[0].path, "store.go") || ops[0].search == "" {
		t.Fatalf("edit op: %+v", ops[0])
	}
	if ops[1].kind != "write" || ops[1].content == "" {
		t.Fatalf("write op: %+v", ops[1])
	}
}

func TestApplyNativeSearchReplace_unique(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	if err := os.WriteFile(path, []byte("package p\n\nconst beta = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	msg, err := applyNativeSearchReplace(path, "const beta = 1", "const gamma = 2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "1 search/replace") {
		t.Fatalf("msg=%q", msg)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "package p\n\nconst gamma = 2\n" {
		t.Fatalf("got %q", data)
	}
}

func TestApplyNativeSearchReplace_notFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	_ = os.WriteFile(path, []byte("x\n"), 0644)
	_, err := applyNativeSearchReplace(path, "missing", "y")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStateRunner_executeNativeEdit_stripsMarkdownFencesOnWrite(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	workDir := filepath.Join(dir, rig, "mayor", "rig")
	layout := filepath.Join(workDir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{"linkshelf/internal/store/store.go"}
	v.BeadTitleContains = "Implement"
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{NativeEditTools: true, CmdGuard: "implementation", Track: "implementation"},
		Validation: v,
	}
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-store"
	stubImplementBeadsHook(t, orchestrator.PlanBead{
		ID:    "te-store",
		Title: "Implement linkshelf/internal/store/store.go per architecture",
	})
	var combined strings.Builder
	body := "```go\npackage store\n\nimport \"fmt\"\n```\nEOF\n"
	ops := []nativeEditOp{{kind: "write", path: "linkshelf/internal/store/store.go", content: body}}
	r.executeNativeEdits(ops, rigMayorRigDir(dir, rig), "", nil, &combined)
	if r.track.hadCmdFailure {
		t.Fatalf("feedback:\n%s", combined.String())
	}
	data, err := os.ReadFile(filepath.Join(workDir, "linkshelf/internal/store/store.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "```") || strings.Contains(string(data), "EOF") {
		t.Fatalf("fences/EOF written to disk: %q", data)
	}
	if !strings.HasPrefix(string(data), "package store") {
		t.Fatalf("got %q", data)
	}
}

func TestStateRunner_executeNativeEdit_writeAndScope(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	workDir := filepath.Join(dir, rig, "mayor", "rig")
	layout := filepath.Join(workDir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{"linkshelf/internal/foo.go"}
	v.BeadTitleContains = "Implement"

	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{
			NativeEditTools: true,
			CmdGuard:        "implementation",
			Track:           "implementation",
		},
		Validation: v,
	}
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-foo"
	stubImplementBeadsHook(t, orchestrator.PlanBead{
		ID:    "te-foo",
		Title: "Implement linkshelf/internal/foo.go per architecture",
	})

	var combined strings.Builder
	ops := []nativeEditOp{{
		kind:    "write",
		path:    "linkshelf/internal/foo.go",
		content: "package foo\n",
	}}
	r.executeNativeEdits(ops, rigMayorRigDir(dir, rig), "", nil, &combined)
	if r.track.hadCmdFailure {
		t.Fatalf("feedback:\n%s", combined.String())
	}
	data, err := os.ReadFile(filepath.Join(workDir, "linkshelf/internal/foo.go"))
	if err != nil || string(data) != "package foo\n" {
		t.Fatalf("file: %v %q", err, data)
	}
}

func TestValidateImplementWritePath_rejectsClosedHeredoc(t *testing.T) {
	// Uses same helpers as production; minimal fixture in orchestrator tests if needed.
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	reason := orchestrator.RejectFullFileHeredocReason(
		"cat > linkshelf/internal/store/store.go <<'EOF'",
		t.TempDir(), "mockrig", "te-x", v,
	)
	// No file on disk — reason empty; create file and prefer incremental
	dir := t.TempDir()
	rig := "mockrig"
	rel := "linkshelf/internal/store/store.go"
	abs := filepath.Join(dir, rig, "mayor", "rig", rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	body := "package store\n\nimport \"errors\"\n\ntype Store struct{}\n\nfunc (s *Store) AddLink(url string) error {\n\tif url == \"\" {\n\t\treturn errors.New(\"empty\")\n\t}\n\treturn nil\n}\n" + strings.Repeat("// padding padding padding\n", 200)
	if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{rel}
	if !orchestrator.PreferIncrementalEdit(dir, rig, rel, v) {
		t.Fatal("fixture should prefer incremental edit")
	}
	reason = orchestrator.RejectFullFileHeredocReason(
		"cat > "+rel+" <<'EOF'", dir, rig, "te-store", v,
	)
	if reason == "" {
		t.Fatal("expected reject full replace on existing file")
	}
	_ = reason
}

func TestRigFlowImplementationHook_nativeEditToolsEnabled(t *testing.T) {
	t.Parallel()
	hooks, err := orchestrator.RigFlowStateHooks("implementation")
	if err != nil {
		t.Fatal(err)
	}
	if !hooks.NativeEditTools {
		t.Fatal("rig-flow implementation must set native_edit_tools: true")
	}
	if section := hooks.NativeEditPromptSection(); section == "" || !strings.Contains(section, "READ:") {
		t.Fatalf("expected native edit prompt section, got %q", section)
	}
}

func TestNativeEdit_READ_returnsFileContent(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	storeBody := "package store\n\nfunc Foo() int { return 1 }\n"
	writeLinkshelfStoreTree(t, mayor, storeBody)
	v := linkshelfImplementValidation("linkshelf/internal/store/store.go")
	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-store"
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "te-store", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	var combined strings.Builder
	had, _, _ := r.processOrchestratedTools("READ: linkshelf/internal/store/store.go\n", "sess", &combined)
	if !had {
		t.Fatal("expected native tool run")
	}
	got := combined.String()
	if !strings.Contains(got, "package store") || !strings.Contains(got, "Foo()") {
		t.Fatalf("READ feedback missing file body:\n%s", got)
	}
}

func TestNativeEdit_autoReadAfterSearchMiss(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 2, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	dir := t.TempDir()
	rig := "mockrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	apiDir := filepath.Join(mayor, "linkshelf/internal/api")
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "package api\n\nfunc TestX(t *testing.T) {}\n"
	testPath := filepath.Join(apiDir, "handlers_test.go")
	if err := os.WriteFile(testPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := linkshelfImplementValidation("linkshelf/internal/api/handlers_test.go")
	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-test"
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "te-test", Title: "Implement linkshelf/internal/api/handlers_test.go per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	response := `EDIT: linkshelf/internal/api/handlers_test.go
<<<<<<< SEARCH
func TestMissing(t *testing.T) {}
=======
func TestY(t *testing.T) {}
>>>>>>> REPLACE
`
	var combined strings.Builder
	hadNative, ok, _ := r.processOrchestratedTools(response, "sess", &combined)
	if !hadNative || ok {
		t.Fatalf("hadNative=%v ok=%v", hadNative, ok)
	}
	if !r.attemptEditSearchMiss {
		t.Fatal("expected attemptEditSearchMiss")
	}
	got := combined.String()
	if !strings.Contains(got, "SEARCH block not found") || !strings.Contains(got, "Auto-READ") {
		t.Fatalf("feedback:\n%s", got)
	}
	if !strings.Contains(got, "func TestX") {
		t.Fatalf("auto-read should include file body:\n%s", got)
	}
	msg, reject := r.rejectImplementationNoOpFailure("failure")
	if !reject || !strings.Contains(msg, "Auto-READ") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}
}

func TestNativeEdit_EDIT_rejectsClosedBeadPath(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	handlers := filepath.Join(mayor, "linkshelf/internal/api")
	if err := os.MkdirAll(handlers, 0755); err != nil {
		t.Fatal(err)
	}
	// Substantive file so stub heuristics do not trigger auto-reopen of the closed handlers bead.
	body := "package api\n\nimport \"net/http\"\n\n// Handlers registers HTTP routes for the Link Shelf API.\nfunc Register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"/api/links\", handleLinks)\n}\n\nfunc handleLinks(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(http.StatusOK)\n}\n"
	if err := os.WriteFile(filepath.Join(handlers, "handlers.go"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := linkshelfImplementValidation(
		"linkshelf/internal/api/handlers.go",
		"linkshelf/cmd/server/main.go",
	)
	closedHandlersBeadHook()
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-main"

	response := `EDIT: linkshelf/internal/api/handlers.go
<<<<<<< SEARCH
func handleLinks(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
=======
func handleLinks(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
>>>>>>> REPLACE
`
	var combined strings.Builder
	r.processOrchestratedTools(response, "sess", &combined)
	if !r.track.hadCmdFailure {
		t.Fatal("expected failure tracking for closed-path EDIT")
	}
	got := combined.String()
	if !strings.Contains(got, "closed") {
		t.Fatalf("want closed-bead rejection, got:\n%s", got)
	}
	data, _ := os.ReadFile(filepath.Join(handlers, "handlers.go"))
	if strings.Contains(string(data), "StatusNoContent") {
		t.Fatal("closed file must not be modified")
	}
}

func TestNativeEdit_autoVerifyAfterEDIT(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	rig := "testrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	writeLinkshelfStoreTree(t, mayor, "pacakge store\n")
	v := linkshelfImplementValidation("linkshelf/internal/store/store.go")
	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-store"
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "te-store", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	response := `EDIT: linkshelf/internal/store/store.go
<<<<<<< SEARCH
pacakge store
=======
package store
>>>>>>> REPLACE
`
	var combined strings.Builder
	hadNative, _, cmdCount := r.processOrchestratedTools(response, "sess", &combined)
	if !hadNative || cmdCount != 0 {
		t.Fatalf("hadNative=%v cmdCount=%d", hadNative, cmdCount)
	}
	if r.track.hadCmdFailure || !r.track.verifyOK {
		t.Fatalf("verifyOK=%v hadCmdFailure=%v feedback:\n%s", r.track.verifyOK, r.track.hadCmdFailure, combined.String())
	}
	if !strings.Contains(combined.String(), "Auto-verify (after native edit)") {
		t.Fatalf("expected auto-verify feedback:\n%s", combined.String())
	}
	data, _ := os.ReadFile(filepath.Join(mayor, "linkshelf/internal/store/store.go"))
	if !strings.HasPrefix(string(data), "package store") {
		t.Fatalf("file not fixed: %q", data)
	}
}

func TestOrchestratedTurn_nativeEditThenCMD_integration(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	writeLinkshelfStoreTree(t, mayor, "package store\n\nvar Broken = 1\n")
	v := linkshelfImplementValidation("linkshelf/internal/store/store.go")
	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-store"
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "te-store", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	response := `READ: linkshelf/internal/store/store.go

EDIT: linkshelf/internal/store/store.go
<<<<<<< SEARCH
var Broken = 1
=======
var Fixed = 1
>>>>>>> REPLACE

CMD: true
`
	var combined strings.Builder
	hadNative, _, cmdCount := r.processOrchestratedTools(response, "sess", &combined)
	if !hadNative || cmdCount != 1 {
		t.Fatalf("hadNative=%v cmdCount=%d", hadNative, cmdCount)
	}
	got := combined.String()
	if !strings.Contains(got, "READ:") || !strings.Contains(got, "EDIT:") || !strings.Contains(got, "package store") {
		t.Fatalf("unexpected feedback:\n%s", got)
	}
	data, _ := os.ReadFile(filepath.Join(mayor, "linkshelf/internal/store/store.go"))
	if !strings.Contains(string(data), "var Fixed = 1") {
		t.Fatalf("disk file not updated: %s", data)
	}
}

func TestNativeEdit_WRITE_rejectsChdirInHandlerTest(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(mayor, "linkshelf/internal/api"), 0755); err != nil {
		t.Fatal(err)
	}
	webDir := filepath.Join(mayor, "linkshelf/web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!DOCTYPE html><html></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	v := linkshelfImplementValidation(
		"linkshelf/internal/api/handlers_test.go",
		"linkshelf/web/index.html",
		"linkshelf/cmd/server/main.go",
	)
	stubImplementBeadsHook(t, orchestrator.PlanBead{
		ID:    "te-rnd",
		Title: "Implement linkshelf/internal/api/handlers_test.go per architecture",
	})
	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-rnd"

	response := `WRITE: linkshelf/internal/api/handlers_test.go
package api

import (
	"os"
	"testing"
)

func TestStatic(t *testing.T) {
	os.Chdir(t.TempDir())
}
---END WRITE---
`
	var combined strings.Builder
	hadNative, ok, _ := r.processOrchestratedTools(response, "sess", &combined)
	if ok {
		t.Fatal("expected chdir rejection")
	}
	if !hadNative {
		t.Fatal("expected native tool attempt")
	}
	if !strings.Contains(combined.String(), "os.Chdir") {
		t.Fatalf("feedback:\n%s", combined.String())
	}
}

func TestValidateAndNormalizeNativeGoContent_dedupesDuplicateTests(t *testing.T) {
	t.Parallel()
	body := `package pkg

import "testing"

func TestWidget(t *testing.T) {}

func TestWidget(t *testing.T) { t.Fatal("dup") }
`
	out, err := validateAndNormalizeNativeGoContent("app/internal/pkg/widget_test.go", body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "func TestWidget") != 1 {
		t.Fatalf("want one TestWidget, got:\n%s", out)
	}
}
