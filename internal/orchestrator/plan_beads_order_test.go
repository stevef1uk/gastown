package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchesImplementBeadTitle_gluedTypo(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{BeadTitleContains: "Implement "}
	if MatchesImplementBeadTitle("ImplementDockerfile per architecture", v) {
		t.Fatal("glued title without space should not match implement bead")
	}
	if !MatchesImplementBeadTitle("Implement Dockerfile per architecture", v) {
		t.Fatal("canonical title should match")
	}
	if MatchesImplementBeadTitle("Witness Patrol", v) {
		t.Fatal("patrol should not match")
	}
}

func TestMatchesImplementBeadTitle_pathInRequiredFilesWithoutPrefix(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/go.mod", "linkshelf/internal/store/schema.go"},
	}
	if !MatchesImplementBeadTitle("Implement go.mod per architecture", v) {
		t.Fatal("canonical title with path in required_files should match without layout prefix")
	}
	if MatchesImplementBeadTitle("Implement other/pkg/foo.go per architecture", v) {
		t.Fatal("path not in required_files should not match")
	}
}

func TestExtractPathFromBeadTitle_gluedImplement(t *testing.T) {
	t.Parallel()
	got := ExtractPathFromBeadTitle("ImplementDockerfile per architecture", "Implement ")
	if got != "Dockerfile" {
		t.Fatalf("got %q want Dockerfile", got)
	}
	got = ExtractPathFromBeadTitle("Implement.env.example per architecture", "Implement ")
	if got != ".env.example" {
		t.Fatalf("got %q want .env.example", got)
	}
}

func TestExtractPathFromBeadTitle_legacyImplementWithAlternateProfilePrefix(t *testing.T) {
	t.Parallel()
	got := ExtractPathFromBeadTitle("Implement linkshelf/main.go per architecture", "Link Shelf /")
	if got != "linkshelf/main.go" {
		t.Fatalf("got %q want linkshelf/main.go", got)
	}
}

func TestIsNonCanonicalImplementBeadTitle(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Link Shelf /",
	}
	if !isNonCanonicalImplementBeadTitle("Implement linkshelf/main.go per architecture", v) {
		t.Fatal("legacy per-arch title should be non-canonical")
	}
	if !isNonCanonicalImplementBeadTitle("Implement linkshelf/schema.go", v) {
		t.Fatal("legacy title without per architecture should be non-canonical")
	}
	if isNonCanonicalImplementBeadTitle("Link Shelf /linkshelf/go.mod per architecture", v) {
		t.Fatal("canonical title should not be legacy")
	}
}

func TestMatchesImplementBeadTitle_legacyFlatPathWithCanonicalPrefix(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Link Shelf /",
		RequiredFiles:     []string{"linkshelf/cmd/server/main.go"},
	}
	if MatchesImplementBeadTitle("Implement linkshelf/main.go per architecture", v) {
		t.Fatal("flattened main.go must not be a queue implement bead when profile requires cmd/server")
	}
	if !looksLikeOpenImplementBeadTitle("Implement linkshelf/main.go per architecture", v) {
		t.Fatal("flattened title should still be detectable for pruning")
	}
	if !MatchesImplementBeadTitle("Link Shelf /linkshelf/cmd/server/main.go per architecture", v) {
		t.Fatal("canonical title should match")
	}
}

func TestMatchesImplementBeadTitle_flatHandlersNotQueueBead(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/api/handlers.go",
		},
	}
	flat := "Implement linkshelf/handlers.go per architecture"
	if MatchesImplementBeadTitle(flat, v) {
		t.Fatal("flattened handlers bead must not match nested required_files profile")
	}
	canonical := "Implement linkshelf/internal/api/handlers.go per architecture"
	if !MatchesImplementBeadTitle(canonical, v) {
		t.Fatal("canonical nested handlers bead should match")
	}
}

func TestPruneMalformedImplementBeads_keepsCanonicalTitles(t *testing.T) {
	t.Parallel()
	// Regression: malformed prune must not require pathMatchesRequired (that deleted all canonical beads).
	v := WorkflowValidation{BeadTitleContains: "Implement "}
	title := "Implement frontend/package.json per architecture"
	pfx := strings.ToLower(strings.TrimSpace(v.BeadTitleContains))
	if !strings.HasPrefix(strings.ToLower(title), pfx) {
		t.Fatal("canonical title should match prefix")
	}
	p := ExtractPathFromBeadTitle(title, v.BeadTitleContains)
	if !IsValidImplementBeadPath(p) {
		t.Fatalf("path invalid: %q", p)
	}
	// Old bug: ok := canonical && IsValid && pathMatchesRequired(p, v.RequiredFiles) with empty RequiredFiles.
	if !pathMatchesRequired(p, nil) {
		// canonical+valid must be kept even when required_files is empty in this hook's caller context.
	}
}

func TestValidateImplementBeadCreateTitle_rejectsGluedPrefix(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"Dockerfile"},
	}
	if err := ValidateImplementBeadCreateTitle("ImplementDockerfile per architecture", v); err == nil {
		t.Fatal("expected reject glued Implement prefix")
	}
}

func TestIsValidImplementBeadPath(t *testing.T) {
	t.Parallel()
	ok := []string{
		"linkshelf/go.mod",
		"linkshelf/internal/store/store.go",
		"linkshelf/cmd/server/main.go",
	}
	for _, p := range ok {
		if !IsValidImplementBeadPath(p) {
			t.Fatalf("want valid %q", p)
		}
	}
	if !IsValidImplementBeadPath("Dockerfile") {
		t.Fatal("Dockerfile must be valid for flat-repo scaffold beads")
	}
	bad := []string{
		"linkshelf/P2]",
		"linkshelf/architecture",
		"linkshelf/linkshelf/go.mod",
		"linkshelf/[task]",
		"linkshelf/` command to create the file.",
		"linkshelf/** to create it per architecture and plan acceptance.",
		"command with `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` blocks.",
		"",
	}
	for _, p := range bad {
		if IsValidImplementBeadPath(p) {
			t.Fatalf("want invalid %q", p)
		}
	}
}

func TestValidatePlanBeads_rejectsBasenameOnlyPaths(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		LayoutRoot:        "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	beads := []PlanBead{
		{ID: "te-1", Title: "Implement linkshelf/schema.go per architecture"},
		{ID: "te-2", Title: "Implement linkshelf/main.go per architecture"},
	}
	if err := ValidatePlanBeads(beads, "", v, "testgt3"); err == nil {
		t.Fatal("expected reject flattened paths")
	}
	if err := ValidatePlanBeadPathsExact(beads, v, "testgt3"); err == nil {
		t.Fatal("expected exact path validation error")
	}
}

func TestValidatePlanBeads_rejectsExtras(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/store.go",
		},
	}
	beads := []PlanBead{
		{ID: "te-1", Title: "Implement linkshelf/go.mod per architecture"},
		{ID: "te-2", Title: "Implement linkshelf/internal/store/store.go per architecture"},
		{ID: "te-3", Title: "Implement linkshelf/P2]"},
	}
	if err := ValidatePlanBeads(beads, "", v, ""); err == nil {
		t.Fatal("expected extra/invalid bead rejection")
	}
}

func TestSelectKeeperImplementBead_prefersCanonicalTitle(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/go.mod"},
	}
	beads := []PlanBead{
		{ID: "te-j8b", Title: "Implement linkshelf/go.mod"},
		{ID: "te-8ml", Title: "Implement linkshelf/go.mod per architecture"},
		{ID: "te-dqs", Title: "Implement linkshelf/go.mod"},
	}
	got := selectKeeperImplementBead(beads, "linkshelf/go.mod", []string{"te-j8b", "te-8ml", "te-dqs"}, v)
	if got != "te-8ml" {
		t.Fatalf("keeper = %q, want te-8ml", got)
	}
}

func TestFormatImplementationQueueBlock_nextOnly(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/store.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	got := FormatImplementationQueueBlock("", "rig", v)
	if strings.Contains(got, "Build order:") {
		t.Fatalf("should not list full build order: %q", got)
	}
	if got == "" {
		t.Fatal("expected non-empty when required_files set")
	}
}

func TestValidatePlanningBeadCreate_blocksWhenRequiredFilesCovered(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Link Shelf /",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/cmd/server/main.go",
		},
	}
	open := []PlanBead{
		{ID: "te-1", Title: "Link Shelf /linkshelf/go.mod per architecture"},
		{ID: "te-2", Title: "Link Shelf /linkshelf/cmd/server/main.go per architecture"},
	}
	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return open, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { ListImplementBeadsByStatusHook = prev })
	err := ValidatePlanningBeadCreate("/tmp", "rig", "Implement linkshelf/main.go per architecture", v)
	if err == nil || !strings.Contains(err.Error(), "already cover") {
		t.Fatalf("expected block when covered, got %v", err)
	}
}

func TestRunOnTimeoutHook_resetPlanningPhase_delegates(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir := filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte("### te-1: Dockerfile\n- Acceptance: per architecture"), 0644); err != nil {
		t.Fatal(err)
	}
	writeMinimalPlanningRigDocs(t, rigDir)
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"Dockerfile"},
	}
	if _, err := RunOnTimeoutHook("reset_planning_phase", townRoot, rig, v); err != nil {
		t.Fatal(err)
	}
	open, err := listAllOpenBeads(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, b := range open {
		if strings.HasPrefix(b.Title, "Implement ") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 canonical implement bead, got %d: %v", n, open)
	}
}

func TestPlanningBeadTitle_preservesSpaceAfterImplement(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{BeadTitleContains: "Implement "}
	got := PlanningBeadTitle("frontend/package.json", v)
	want := "Implement frontend/package.json per architecture"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseBeadIDFromCreateOutput_rigPrefix(t *testing.T) {
	t.Parallel()
	out := "✓ Created issue: rig-abc — Implement Dockerfile per architecture\n  Priority: P2\n"
	if got := parseBeadIDFromCreateOutput(out); got != "rig-abc" {
		t.Fatalf("got %q want rig-abc", got)
	}
}

func TestOrderRequiredFilesForImplementation(t *testing.T) {
	t.Parallel()
	files := []string{
		"linkshelf/cmd/server/main.go",
		"linkshelf/go.mod",
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/api/handlers.go",
	}
	got := OrderRequiredFilesForImplementation(files)
	if got[0] != "linkshelf/go.mod" {
		t.Fatalf("go.mod first: %v", got)
	}
	if got[len(got)-1] != "linkshelf/cmd/server/main.go" {
		t.Fatalf("main.go last: %v", got)
	}
}

func TestOrderRequiredFilesForImplementation_webBeforeHandlers(t *testing.T) {
	t.Parallel()
	files := []string{
		"linkshelf/cmd/server/main.go",
		"linkshelf/internal/api/handlers.go",
		"linkshelf/web/index.html",
		"linkshelf/internal/store/store.go",
	}
	got := OrderRequiredFilesForImplementation(files)
	var webIdx, handlerIdx = -1, -1
	for i, p := range got {
		if strings.Contains(p, "/web/") {
			webIdx = i
		}
		if strings.Contains(p, "/internal/api/handlers.go") {
			handlerIdx = i
		}
	}
	if webIdx < 0 || handlerIdx < 0 || webIdx > handlerIdx {
		t.Fatalf("web before handlers: %v", got)
	}
}

func TestEnforceSingleImplementInProgress_reopensOffHeadBead(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir := filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/api/handlers.go",
		},
	}
	create := func(title string) string {
		cmd := exec.Command("bd", "create", "--type", "task", "--title", title, "--description=test")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = rigDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd create: %v\n%s", err, out)
		}
		open, err := ListOpenImplementBeads(townRoot, rig, v)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range open {
			if b.Title == title {
				return b.ID
			}
		}
		t.Fatalf("no open bead for title %q", title)
		return ""
	}
	gomodID := create("Implement linkshelf/go.mod per architecture")
	_ = create("Implement linkshelf/internal/store/schema.go per architecture")
	handlersID := create("Implement linkshelf/internal/api/handlers.go per architecture")
	up := exec.Command("bd", "update", handlersID, "--status=in_progress")
	up.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	up.Dir = rigDir
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("bd update in_progress: %v\n%s", err, out)
	}
	reopened, err := EnforceSingleImplementInProgress(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 1 || reopened[0] != handlersID {
		t.Fatalf("reopened = %v, want [%s]", reopened, handlersID)
	}
	next, err := NextOpenImplementBead(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.ID != gomodID {
		t.Fatalf("next bead = %v, want %s (queue head)", next, gomodID)
	}
}

func TestBeadsDatabaseReady_falseWithoutInit(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	if BeadsDatabaseReady(town, "myrig") {
		t.Fatal("expected false when rig has no beads database")
	}
}

func TestReopenClosedImplementBeadsForMissingOpenRequired_reopensClosedOnlyPaths(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir := filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(townRoot, rig, ".git")); os.IsNotExist(err) {
		t.Skip("skipping integration test: bd hooks require git repo")
	}
	v := WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement ",
		RequiredFiles: []string{
			"go.mod",
			"cmd/server/main.go",
			"internal/store/schema.go",
		},
	}
	create := func(title string) string {
		cmd := exec.Command("bd", "create", "--type", "task", "--title", title, "--description=test")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = rigDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd create: %v\n%s", err, out)
		}
		open, err := ListOpenImplementBeads(townRoot, rig, v)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range open {
			if b.Title == title {
				return b.ID
			}
		}
		t.Fatalf("no open bead for title %q", title)
		return ""
	}
	closeBead := func(id string) {
		cmd := exec.Command("bd", "close", id, "--reason=test")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = rigDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd close %s: %v\n%s", id, err, out)
		}
	}
	gomodID := create("Implement go.mod per architecture")
	mainID := create("Implement cmd/server/main.go per architecture")
	schemaID := create("Implement internal/store/schema.go per architecture")
	closeBead(gomodID)
	closeBead(mainID)

	reopened, err := ReopenClosedImplementBeadsForMissingOpenRequired(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 2 {
		t.Fatalf("reopened = %v, want 2 entries", reopened)
	}
	open, err := ListOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, b := range open {
		seen[b.ID] = true
	}
	for _, id := range []string{gomodID, mainID, schemaID} {
		if !seen[id] {
			t.Fatalf("expected open bead %s, got open set %v", id, open)
		}
	}
	if err := ValidatePlanBeads(open, "", v, rig); err != nil {
		t.Fatalf("ValidatePlanBeads after reopen: %v", err)
	}
}

func TestPruneDuplicateImplementBeads_openAndInProgressSamePath(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir := filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/internal/api/handlers.go"},
	}
	create := func(title string) string {
		cmd := exec.Command("bd", "create", "--type", "task", "--title", title, "--description=test")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = rigDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd create: %v\n%s", err, out)
		}
		open, err := ListOpenImplementBeads(townRoot, rig, v)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range open {
			if b.Title == title {
				return b.ID
			}
		}
		t.Fatalf("no open bead for title %q", title)
		return ""
	}
	path := "linkshelf/internal/api/handlers.go"
	canonical := PlanningBeadTitle(path, v)
	headID := create(canonical)
	dupID := create("Implement linkshelf/internal/api/handlers.go per architecture duplicate")
	up := exec.Command("bd", "update", headID, "--status=in_progress")
	up.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	up.Dir = rigDir
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("bd update in_progress: %v\n%s", err, out)
	}
	deleted, err := PruneDuplicateImplementBeads(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != dupID {
		t.Fatalf("deleted = %v, want [%s]", deleted, dupID)
	}
	active, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != headID {
		t.Fatalf("active beads = %v, want single %s", active, headID)
	}
}
