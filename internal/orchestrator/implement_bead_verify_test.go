package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeMinimalGoModule(t *testing.T, rigDir string) {
	t.Helper()
	layout := filepath.Join(rigDir, "app")
	storeDir := filepath.Join(layout, "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module app\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte("package store\n\nfunc List() int { return 0 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store\n\nimport \"testing\"\n\nfunc TestList(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalPythonProject(t *testing.T, rigDir string) {
	t.Helper()
	layout := filepath.Join(rigDir, "app")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "main.py"), []byte("def main():\n    return 42\n\nif __name__ == '__main__':\n    print(main())\n"), 0644); err != nil {
		t.Fatal(err)
	}
	venvBin := filepath.Join(rigDir, ".venv", "bin")
	if err := os.MkdirAll(venvBin, 0755); err != nil {
		t.Fatal(err)
	}
	sysPython, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sysPython, filepath.Join(venvBin, "python3")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
}

func TestCloseImplementBeadsWithGreenGoVerify_profileOrder(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles: []string{
			"app/go.mod",
			"app/internal/store/store.go",
			"app/internal/store/store_test.go",
		},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	defer func() { bdCloseImplementBeadHook = nil }()

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "open":
			return []PlanBead{
				{ID: "b-store", Title: "Implement app/internal/store/store.go per architecture"},
				{ID: "b-test", Title: "Implement app/internal/store/store_test.go per architecture"},
			}, nil
		default:
			return nil, nil
		}
	}
	defer func() { ListImplementBeadsByStatusHook = prev }()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	got, err := CloseImplementBeadsWithGreenGoVerify(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("closed=%v want b-store and b-test", got)
	}
	if got[0] != "b-store" || got[1] != "b-test" {
		t.Fatalf("profile order: got %v", got)
	}
}

func TestCloseImplementBeadsWithGreenGoVerify_autoClosesGoModBead(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	layout := filepath.Join(rigDir, "app")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module app\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/go.mod"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	defer func() { bdCloseImplementBeadHook = nil }()

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "b-mod", Title: "Implement app/go.mod per architecture"}}, nil
		}
		return nil, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prev }()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	if !eval.GoSatisfied("app/go.mod") {
		t.Fatal("GoSatisfied(app/go.mod) want true")
	}
	got, err := CloseImplementBeadsWithGreenGoVerify(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "b-mod" {
		t.Fatalf("closed=%v want b-mod", got)
	}
}

func TestReopenClosedImplementBeadsOrdered_skipsGreenVerify(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "b1", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prev }()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	reopened, err := reopenClosedImplementBeadsOrdered(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 0 {
		t.Fatalf("reopened=%v want none when go test passes", reopened)
	}
}

func TestReopenClosedImplementBeadsOrdered_reopensWhenGoVerifyFails(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)
	if err := os.WriteFile(filepath.Join(rigDir, "app", "internal", "store", "store.go"), []byte("package store\n\nfunc List() int { return broken }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "b1", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	prevUpdate := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(townRoot, rig, beadID, status string) error {
		return nil
	}
	defer func() {
		ListImplementBeadsByStatusHook = prev
		bdUpdateImplementBeadStatusHook = prevUpdate
	}()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	reopened, err := reopenClosedImplementBeadsOrdered(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 1 || reopened[0] != "b1" {
		t.Fatalf("reopened=%v want [b1] when go test fails", reopened)
	}
}

func TestReopenClosedImplementBeadsOrdered_skipsPythonGreenVerify(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalPythonProject(t, rigDir)

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		PythonVenvDir:     ".venv",
		RequiredFiles:     []string{"app/main.py"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "b1", Title: "Implement app/main.py per architecture"}}, nil
		}
		return nil, nil
	}
	prevUpdate := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(townRoot, rig, beadID, status string) error {
		return nil
	}
	defer func() {
		ListImplementBeadsByStatusHook = prev
		bdUpdateImplementBeadStatusHook = prevUpdate
	}()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	reopened, err := reopenClosedImplementBeadsOrdered(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 0 {
		t.Fatalf("reopened=%v want none when python verify passes", reopened)
	}
}

func TestReopenClosedImplementBeadsOrdered_reopensWhenPythonVerifyFails(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalPythonProject(t, rigDir)
	if err := os.WriteFile(filepath.Join(rigDir, "app", "main.py"), []byte("def main(\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		PythonVenvDir:     ".venv",
		RequiredFiles:     []string{"app/main.py"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "b1", Title: "Implement app/main.py per architecture"}}, nil
		}
		return nil, nil
	}
	prevUpdate := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(townRoot, rig, beadID, status string) error {
		return nil
	}
	defer func() {
		ListImplementBeadsByStatusHook = prev
		bdUpdateImplementBeadStatusHook = prevUpdate
	}()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	reopened, err := reopenClosedImplementBeadsOrdered(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 1 || reopened[0] != "b1" {
		t.Fatalf("reopened=%v want [b1] when python verify fails", reopened)
	}
}

func TestReopenClosedImplementBeadsForMissingOpenRequired_reopensGreenGoVerify(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "b1", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	var reopenedIDs []string
	prevUpdate := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(townRoot, rig, beadID, status string) error {
		reopenedIDs = append(reopenedIDs, beadID)
		return nil
	}
	defer func() {
		ListImplementBeadsByStatusHook = prev
		bdUpdateImplementBeadStatusHook = prevUpdate
	}()

	reopened, err := ReopenClosedImplementBeadsForMissingOpenRequired(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) == 0 {
		t.Fatal("closed bead still in required_files must be reopened even when verify passes")
	}
}

func TestReopenClosedImplementBeadsForMissingOpenRequired_reopensWhenGoVerifyFails(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)
	if err := os.WriteFile(filepath.Join(rigDir, "app", "internal", "store", "store.go"), []byte("package store\n\nfunc List() int { return broken }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "b1", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	var reopenedIDs []string
	prevUpdate := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(townRoot, rig, beadID, status string) error {
		reopenedIDs = append(reopenedIDs, beadID)
		return nil
	}
	defer func() {
		ListImplementBeadsByStatusHook = prev
		bdUpdateImplementBeadStatusHook = prevUpdate
	}()

	reopened, err := ReopenClosedImplementBeadsForMissingOpenRequired(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 1 || reopened[0] != "b1 (app/internal/store/store.go)" {
		t.Fatalf("reopened=%v want [b1 (app/internal/store/store.go)]", reopened)
	}
	if len(reopenedIDs) != 1 || reopenedIDs[0] != "b1" {
		t.Fatalf("bdUpdate called=%v want [b1]", reopenedIDs)
	}
}

func TestImplementBeadVerifyEvaluator_memoizes(t *testing.T) {
	dir := t.TempDir()
	rigDir := dir
	writeMinimalGoModule(t, rigDir)
	v := WorkflowValidation{
		LayoutRoot:        "app",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}
	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	p := "app/internal/store/store.go"
	if !eval.GoSatisfied(p) {
		t.Skip("go toolchain required")
	}
	if !eval.GoSatisfied(p) {
		t.Fatal("second call should hit memo")
	}
	if len(eval.memo) != 1 {
		t.Fatalf("memo=%d want 1 verify run per path", len(eval.memo))
	}
}

func TestCloseImplementBeadsWithGreenGoVerify_autoClosesFrontendArtifact(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)
	webDir := filepath.Join(rigDir, "app", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	html := "<!DOCTYPE html>\n<html>\n<body>\n<ul id=\"links\"></ul>\n</body>\n</html>\n"
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement app/",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/web/index.html"},
		MinImplementationFileBytes: 20,
		MinSubstantiveLines:        2,
	}

	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	defer func() { bdCloseImplementBeadHook = nil }()

	prevList := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "zz-html", Title: "Implement app/web/index.html per architecture"}}, nil
		}
		return nil, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prevList }()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	if !eval.VerifySatisfied("app/web/index.html") {
		t.Fatal("frontend artifact should be satisfied")
	}
	got, err := CloseImplementBeadsWithGreenGoVerify(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "zz-html" {
		t.Fatalf("closed=%v want zz-html", got)
	}
}

func TestCloseImplementBeadsWithGreenFrontendVerify_closesInProgress(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)
	webDir := filepath.Join(rigDir, "app", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	css := "/* LinkShelf styles */\nheader { color: #333; }\nmain { padding: 1em; }\n"
	if err := os.WriteFile(filepath.Join(webDir, "style.css"), []byte(css), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement app/",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles: []string{
			"app/web/style.css",
			"app/internal/api/handlers.go",
			"app/cmd/server/main.go",
		},
		MinImplementationFileBytes: 20,
		MinSubstantiveLines:        2,
	}

	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	defer func() { bdCloseImplementBeadHook = nil }()

	prevList := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "in_progress":
			return []PlanBead{{ID: "b-css", Title: "Implement app/web/style.css per architecture"}}, nil
		case "open":
			return []PlanBead{{ID: "b-handlers", Title: "Implement app/internal/api/handlers.go per architecture"}}, nil
		default:
			return nil, nil
		}
	}
	defer func() { ListImplementBeadsByStatusHook = prevList }()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	if !eval.VerifySatisfied("app/web/style.css") {
		t.Fatal("frontend artifact should be satisfied")
	}
	got, err := CloseImplementBeadsWithGreenFrontendVerify(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "b-css" {
		t.Fatalf("closed=%v want b-css only", got)
	}
	if len(closed) != 1 || closed[0] != "b-css" {
		t.Fatalf("bd close hook=%v want b-css", closed)
	}
}

func TestReconcileImplementBeads_autoClosesFrontendWhenPhaseVerifyRed(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)
	webDir := filepath.Join(rigDir, "app", "web")
	apiDir := filepath.Join(rigDir, "app", "internal", "api")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	css := "/* LinkShelf styles */\nheader { color: #333; }\nmain { padding: 1em; }\n"
	if err := os.WriteFile(filepath.Join(webDir, "style.css"), []byte(css), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "handlers.go"), []byte(">>>>>>> REPLACE"), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement app/",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles: []string{
			"app/web/style.css",
			"app/internal/api/handlers.go",
			"app/cmd/server/main.go",
		},
		MinImplementationFileBytes: 20,
		MinSubstantiveLines:        2,
	}

	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	defer func() { bdCloseImplementBeadHook = nil }()

	prevList := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "in_progress":
			return []PlanBead{{ID: "b-css", Title: "Implement app/web/style.css per architecture"}}, nil
		case "open":
			return []PlanBead{{ID: "b-handlers", Title: "Implement app/internal/api/handlers.go per architecture"}}, nil
		default:
			return nil, nil
		}
	}
	defer func() { ListImplementBeadsByStatusHook = prevList }()

	log, err := ReconcileImplementBeads(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "skipped Go auto-close: phase verify not green") {
		t.Fatalf("want Go auto-close skip in log: %q", log)
	}
	if !strings.Contains(log, "auto-closed frontend (verify green): b-css") {
		t.Fatalf("want frontend auto-close in log: %q", log)
	}
	if len(closed) != 1 || closed[0] != "b-css" {
		t.Fatalf("closed=%v want b-css only", closed)
	}
}

func TestReconcileImplementBeads_deterministicLogOrder(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "b1", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prev }()

	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	defer func() { bdCloseImplementBeadHook = nil }()

	log, err := ReconcileImplementBeads(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(log, "auto-closed (verify green):") {
		t.Fatalf("close must run before reopen/audit issues: %q", log)
	}
	if len(closed) != 1 || closed[0] != "b1" {
		t.Fatalf("closed=%v", closed)
	}
}
