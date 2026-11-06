package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStuckClockStartsAtLastProgress(t *testing.T) {
	now := time.Now().UTC()
	in := WorkflowStuckEvalInput{Now: now, Config: WorkflowStuckConfig{IdleAfter: 30 * time.Minute, StateGrace: time.Minute, ReworkLinger: 20 * time.Minute}, Guard: rigFlowStuckGuard(), CurrentState: "implementation", StateEnteredAt: now.Add(-2 * time.Hour).Format(time.RFC3339), LastProgressAt: now.Add(-time.Minute), BeadFingerprint: "closed:b1", LastBeadFingerprint: "closed:b1", PolecatRunning: true, PendingRework: true}
	got := EvalWorkflowStuck(in)
	if containsStuckSignal(got.Signals, SignalPhaseIdleNoBeadProgress) || containsStuckSignal(got.Signals, SignalPendingReworkLinger) {
		t.Fatalf("healthy work marked stuck: %+v", got)
	}
	in.LastProgressAt = now.Add(-40 * time.Minute)
	if !EvalWorkflowStuck(in).Stuck {
		t.Fatal("actual idle work not detected")
	}
}

func TestWorkflowBudgetConcurrentAndDurable(t *testing.T) {
	t.Setenv("GT_WORKFLOW_MAX_LLM_REQUESTS", "7")
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "10000")
	dir := t.TempDir()
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ReserveWorkflowLLMRequest(dir, "renamed", "wf-1", 10) == nil {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 7 {
		t.Fatalf("admitted %d, want 7", admitted.Load())
	}
	if ReserveWorkflowLLMRequest(dir, "renamed", "wf-1", 10) == nil {
		t.Fatal("persisted budget reset")
	}
	if err := ReserveWorkflowLLMRequest(dir, "other", "wf-1", 10); err != nil {
		t.Fatalf("independent rig blocked: %v", err)
	}
}

func TestWorkflowBudgetCorruptionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orchestrator", "execution-budgets.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if ReserveWorkflowLLMRequest(dir, "rig", "wf", 1) == nil {
		t.Fatal("corrupt budget admitted request")
	}
}

func TestMixedLanguageDetection(t *testing.T) {
	v := WorkflowValidation{TestRunner: "go", RequiredFiles: []string{"service/go.mod", "jobs/main.py", "jobs/pyproject.toml", "ui/package.json"}}
	if !WorkflowUsesGo(v) || !WorkflowUsesPython(v) || !WorkflowUsesNodeJS(v) {
		t.Fatal("mixed toolchains must coexist")
	}
}

func TestComponentSetupUsesIndependentRoots(t *testing.T) {
	v := WorkflowValidation{RequiredFiles: []string{"product/service/go.mod", "product/worker/pyproject.toml", "product/worker/uv.lock", "product/client/package.json", "product/client/pnpm-lock.yaml"}}
	if ProjectSetupStackKind(v) != "mixed" {
		t.Fatal("multiple components collapsed")
	}
	cmd := v.ProjectSetupVerifyHint()
	for _, want := range []string{"'product/service'", "go mod download", "'product/worker'", "uv sync", "'product/client'", "pnpm install"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %s: %s", want, cmd)
		}
	}
}

func TestSharedFileRemainsWritableInActivePhase(t *testing.T) {
	v := WorkflowValidation{ActivePhaseIDField: "feature", DeliveryPhases: []DeliveryPhase{{ID: "foundation", RequiredFiles: []string{"app/main.py"}}, {ID: "feature", RequiredFiles: []string{"app/main.py"}}}}
	if v.FileInCompletedPhase("app/main.py") {
		t.Fatal("shared active file was frozen")
	}
}

func TestProfileGraphRejectsCyclesAndUnknownDependencies(t *testing.T) {
	for _, phases := range [][]DeliveryPhase{
		{{ID: "one", DependsOn: []string{"missing"}}},
		{{ID: "one", DependsOn: []string{"two"}}, {ID: "two", DependsOn: []string{"one"}}},
		{{ID: "one"}, {ID: "one"}},
	} {
		if len(deliveryGraphProblems(WorkflowValidation{DeliveryPhases: phases})) == 0 {
			t.Fatal("invalid graph accepted")
		}
	}
}

func TestResetBeadTurnCountPreservesCheckpointMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "renamed", "qa", "implementation-progress.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	original := `{"workflow_id":"wf-9","phase_id":"foundation","completed":{"closed:b1":true},"bead_turns":{"b1":20,"b2":3},"future_field":"keep"}`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := resetBeadTurnCount(dir, "renamed", "b1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"workflow_id":"wf-9"`, `"phase_id":"foundation"`, `"closed:b1":true`, `"b2":3`, `"future_field":"keep"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("checkpoint field lost: %s", data)
		}
	}
	if strings.Contains(string(data), `"b1":20`) {
		t.Fatal("counter not reset")
	}
}

func TestComponentLockfilesDoNotLeakBetweenPackages(t *testing.T) {
	files := []string{"client/package.json", "tests/package.json", "tests/package-lock.json"}
	cmd := componentSetupCommand(ProjectComponent{Root: "client", Toolchain: "nodejs"}, files)
	if strings.Contains(cmd, "npm ci") {
		t.Fatalf("used another package's lockfile: %s", cmd)
	}
	root := componentSetupCommand(ProjectComponent{Root: ".", Toolchain: "python", Manifest: "pyproject.toml"}, []string{"pyproject.toml", "uv.lock"})
	if !strings.Contains(root, "uv sync") {
		t.Fatalf("root lockfile ignored: %s", root)
	}
}
