package orchestrator

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestPolecatLearnings_ProjectAndTownPersistenceAndStackIsolation(t *testing.T) {
	t.Parallel()
	town := t.TempDir()

	goVal := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:   []string{"linkshelf/go.mod", "linkshelf/internal/store/store.go"},
	}
	pyVal := WorkflowValidation{
		LayoutRoot:      "defender/backend",
		QAVerifyCommand: "python3 -m pytest -q defender/backend",
		TestRunner:      "pytest",
		RequiredFiles:   []string{"defender/backend/main.py", "defender/backend/requirements.txt"},
	}

	// Record a Go learning in rig1
	if err := RecordPolecatLearning(town, "rig1", "", "Initialize empty slice as []Item{} before json.Encode so GET returns [] not null", "agent", "linkshelf/internal/store/store.go", goVal); err != nil {
		t.Fatal(err)
	}
	// Record a Python learning in rig2
	if err := RecordPolecatLearning(town, "rig2", "", "Mount FastAPI StaticFiles after API routes so /api/score is not shadowed", "agent", "defender/backend/main.py", pyVal); err != nil {
		t.Fatal(err)
	}

	// Both project and town files must exist
	if _, err := os.Stat(ProjectLearningsPath(town, "rig1")); err != nil {
		t.Fatalf("expected project learnings file for rig1: %v", err)
	}
	if _, err := os.Stat(TownStackLearningsPath(town, "go")); err != nil {
		t.Fatalf("expected town go learnings file: %v", err)
	}
	if _, err := os.Stat(TownStackLearningsPath(town, "python")); err != nil {
		t.Fatalf("expected town python learnings file: %v", err)
	}

	// A new Go rig (rig3) should inherit the town-scoped Go learning, but NEVER the Python learning
	goBlock := PromptContextBlock("polecat_learnings", town, "rig3", goVal)
	if !strings.Contains(goBlock, "Initialize empty slice as []Item{}") {
		t.Fatalf("expected town Go learning in rig3 prompt block, got:\n%s", goBlock)
	}
	if strings.Contains(goBlock, "FastAPI") {
		t.Fatalf("Go rig must not receive Python town learnings:\n%s", goBlock)
	}

	// A Python rig should see the Python learning, but NEVER the Go learning
	pyBlock := PromptContextBlock("polecat_learnings", town, "rig2", pyVal)
	if !strings.Contains(pyBlock, "Mount FastAPI StaticFiles") {
		t.Fatalf("expected Python learning in rig2 prompt block, got:\n%s", pyBlock)
	}
	if strings.Contains(pyBlock, "[]Item{}") {
		t.Fatalf("Python rig must not receive Go learnings:\n%s", pyBlock)
	}
}

func TestPolecatLearnings_DeduplicationAndRingBufferCap(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	v := WorkflowValidation{
		LayoutRoot:      "app",
		QAVerifyCommand: "go test ./...",
		RequiredFiles:   []string{"app/go.mod", "app/main.go"},
	}

	// Reinforce rule #1 three times
	for i := 0; i < 3; i++ {
		if err := RecordPolecatLearning(town, "myrig", "go", "Always guard in-memory map access with sync.RWMutex in store methods", "agent", "app/main.go", v); err != nil {
			t.Fatal(err)
		}
	}

	// Insert 10 more single-hit rules to trigger ring buffer eviction
	for i := 1; i <= 10; i++ {
		rule := fmt.Sprintf("Single-hit learning rule number %d for testing concise ring buffer eviction", i)
		if err := RecordPolecatLearning(town, "myrig", "go", rule, "verify_fix", "app/main.go", v); err != nil {
			t.Fatal(err)
		}
	}

	store := loadLearningsFile(ProjectLearningsPath(town, "myrig"))
	if len(store.Entries) != MaxProjectLearnings {
		t.Fatalf("expected %d entries after eviction, got %d", MaxProjectLearnings, len(store.Entries))
	}

	// High-hit rule (Hits=3) must survive eviction
	foundHighHit := false
	for _, e := range store.Entries {
		if strings.Contains(e.Rule, "sync.RWMutex") {
			foundHighHit = true
			if e.Hits != 3 {
				t.Fatalf("expected Hits=3, got %d", e.Hits)
			}
		}
	}
	if !foundHighHit {
		t.Fatalf("expected reinforced high-hit rule to survive eviction: %+v", store.Entries)
	}

	block := FormatPolecatLearningsBlock(town, "myrig", v)
	if len(block) > MaxLearningsPromptBytes {
		t.Fatalf("learnings block %d bytes exceeds cap %d", len(block), MaxLearningsPromptBytes)
	}
}

func TestExtractVerifyFixAndQAReworkLearning(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "app", RequiredFiles: []string{"app/go.mod"}}
	got := ExtractVerifyFixLearning("internal/store/store.go:42:12: undefined: store.NewMemoryStore\nFAIL\texample/app [build failed]", "app/internal/store/store.go", v)
	if !strings.Contains(got, "Avoid in store.go") || !strings.Contains(got, "undefined: store.NewMemoryStore") {
		t.Fatalf("unexpected verify fix learning: %q", got)
	}

	rework := ExtractQAReworkLearning("qa_review", "GET /api/bookmarks returned 404 because registerAPI was not called in main.go. Fix cmd/server/main.go.")
	if !strings.Contains(rework, "QA lesson: GET /api/bookmarks returned 404") {
		t.Fatalf("unexpected QA rework learning: %q", rework)
	}
}
