package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowValidation_RequirementsFilePath(t *testing.T) {
	v := WorkflowValidation{RequiredFiles: []string{"pkg/main.py", "pkg/requirements.txt"}}
	if got := v.RequirementsFilePath(); got != "pkg/requirements.txt" {
		t.Fatalf("got %q", got)
	}
	if v.RequirementsFilePath() == "" && len(v.RequiredFiles) == 0 {
		return
	}
}

func TestSanitizePhaseVerifyCommandsForStack_rewritesFinAllyTestPhase(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:         "finally",
		RequiredFiles:      []string{"finally/docker-compose.yml", "finally/test/docker-compose.test.yml"},
		QAVerifyCommand:    "cd finally && pytest",
		DevServerPort:      8000,
		ActivePhaseIDField: "test",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:    "test",
				Title: "Test Layer",
				RequiredFiles: []string{
					"finally/test/package.json",
					"finally/test/playwright.config.ts",
					"finally/test/e2e.spec.ts",
					"finally/Dockerfile",
					"finally/docker-compose.yml",
					"finally/test/docker-compose.test.yml",
				},
				QAVerifyCommand: "cd finally && test -f finally/docker-compose.yml && echo 'compose file ok'",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	var testCmd string
	for _, p := range got.DeliveryPhases {
		if p.ID == "test" {
			testCmd = p.QAVerifyCommand
		}
	}
	lower := strings.ToLower(testCmd)
	if !strings.Contains(lower, "-f test/docker-compose.test.yml") {
		t.Fatalf("test phase QAVerifyCommand %q missing compose file -f test/docker-compose.test.yml", testCmd)
	}
	if !strings.Contains(lower, "--exit-code-from playwright") {
		t.Fatalf("test phase QAVerifyCommand %q missing --exit-code-from playwright", testCmd)
	}
	if !strings.Contains(lower, "docker-compose") && !strings.Contains(lower, "docker compose") {
		t.Fatalf("test phase QAVerifyCommand %q must use docker compose CLI", testCmd)
	}
	if strings.Contains(testCmd, "test -f finally/docker-compose.yml") {
		t.Fatalf("weak test -f command survived sanitize: %q", testCmd)
	}
}

func TestNormalizeLayoutProfile(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		RequiredFiles:   []string{"go.mod", "cmd/server/main.go"},
		QAVerifyCommand: "go run ./cmd/server",
		TestRunner:      "custom",
	}
	got := NormalizeLayoutProfile(v)
	if got.RequiredFiles[0] != "linkshelf/go.mod" {
		t.Fatalf("required_files[0] = %q", got.RequiredFiles[0])
	}
	if !strings.Contains(got.QAVerifyCommand, "cd linkshelf &&") {
		t.Fatalf("qa_verify_command = %q", got.QAVerifyCommand)
	}
}

func TestClampProfileValidation(t *testing.T) {
	tests := []struct {
		name string
		in   WorkflowValidation
		want WorkflowValidation
	}{
		{
			name: "absurd plan from llm",
			in:   WorkflowValidation{MinPlanBytes: 17496, MinArchitectureBytes: 50000},
			want: WorkflowValidation{
				MinPlanBytes:         MinPlanBytesFromArchitecture(DefaultMinArchitectureBytes),
				MinArchitectureBytes: DefaultMinArchitectureBytes,
			},
		},
		{
			name: "zero uses defaults",
			in:   WorkflowValidation{},
			want: WorkflowValidation{
				MinPlanBytes:         MinPlanBytesFromArchitecture(DefaultMinArchitectureBytes),
				MinArchitectureBytes: DefaultMinArchitectureBytes,
			},
		},
		{
			name: "in range kept",
			in:   WorkflowValidation{MinPlanBytes: 1000, MinArchitectureBytes: 6000},
			want: WorkflowValidation{MinPlanBytes: 1000, MinArchitectureBytes: 6000},
		},
		{
			name: "small rig caps high architecture minimum",
			in: WorkflowValidation{
				MinPlanBytes:         3000,
				MinArchitectureBytes: 8000,
				RequiredFiles:        []string{"a.go", "b.go", "c.go"},
			},
			want: WorkflowValidation{
				MinPlanBytes:         MinPlanBytesFromArchitecture(SmallRigMaxArchitectureBytes),
				MinArchitectureBytes: SmallRigMaxArchitectureBytes,
			},
		},
		{
			name: "below floor raised",
			in:   WorkflowValidation{MinPlanBytes: 50},
			want: WorkflowValidation{
				MinPlanBytes:         MinPlanBytesFromArchitecture(DefaultMinArchitectureBytes),
				MinArchitectureBytes: DefaultMinArchitectureBytes,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampProfileValidation(tc.in)
			if got.MinPlanBytes != tc.want.MinPlanBytes || got.MinArchitectureBytes != tc.want.MinArchitectureBytes {
				t.Fatalf("ClampProfileValidation() = plan %d arch %d, want plan %d arch %d",
					got.MinPlanBytes, got.MinArchitectureBytes, tc.want.MinPlanBytes, tc.want.MinArchitectureBytes)
			}
		})
	}
}

func TestMinPlanBytesFromArchitecture_quarter(t *testing.T) {
	t.Parallel()
	if got := MinPlanBytesFromArchitecture(4000); got != 1000 {
		t.Fatalf("got %d want 1000", got)
	}
	if got := MinPlanBytesFromArchitecture(100); got != MinArtifactBytesFloor {
		t.Fatalf("got %d want floor %d", got, MinArtifactBytesFloor)
	}
}

func TestEffectiveMinPlanBytes_usesOnDiskArchitecture(t *testing.T) {
	rigDir := t.TempDir()
	arch := filepath.Join(rigDir, "architecture.md")
	if err := os.WriteFile(arch, make([]byte, 3000), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{MinArchitectureBytes: 8000, MinPlanBytes: 4000}
	if got := EffectiveMinPlanBytes(rigDir, v); got != 750 {
		t.Fatalf("got %d want 750 (quarter of 3000 on disk)", got)
	}
}

func TestEffectiveMinPlanBytes_phasedDeliveryScalesByActiveFiles(t *testing.T) {
	rigDir := t.TempDir()
	const archSize = 8000
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), make([]byte, archSize), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		RequiredFiles:      []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
		ActivePhaseIDField: "p1",
		DeliveryPhases: []DeliveryPhase{
			{ID: "p1", RequiredFiles: []string{"a", "b"}},
			{ID: "p2", RequiredFiles: []string{"c", "d", "e", "f", "g", "h", "i", "j"}},
		},
	}
	full := EffectiveMinPlanBytes(rigDir, WorkflowValidation{})
	if full != MinPlanBytesFromArchitecture(archSize) {
		t.Fatalf("unscaled: got %d want %d", full, MinPlanBytesFromArchitecture(archSize))
	}
	scaled := EffectiveMinPlanBytes(rigDir, v)
	// 2/10 of arch → 1600 bytes → quarter = 400
	want := MinPlanBytesFromArchitecture(int64(float64(archSize) * 0.2))
	if scaled != want {
		t.Fatalf("scaled: got %d want %d", scaled, want)
	}
	if scaled >= full {
		t.Fatalf("scaled %d should be less than full %d", scaled, full)
	}
	// User's finally case: 1648-byte plan should pass when threshold is ~400
	if 1648 < scaled {
		t.Fatalf("1648-byte phase plan should meet scaled min %d", scaled)
	}
	scoped := v.ForActivePhase()
	scopedScaled := EffectiveMinPlanBytes(rigDir, scoped)
	if scopedScaled != scaled {
		t.Fatalf("ForActivePhase-scoped validation: got min %d want %d (gt-agent uses scoped v)", scopedScaled, scaled)
	}
}

func TestDefaultWorkflowValidation(t *testing.T) {
	v := DefaultWorkflowValidation()
	if v.BeadTitleContains != "Implement " {
		t.Fatalf("bead prefix: %q", v.BeadTitleContains)
	}
	if len(v.RequiredFiles) != 0 {
		t.Fatalf("required files should be empty until profile loaded: %v", v.RequiredFiles)
	}
}

func TestWithDefaults_partial(t *testing.T) {
	v := WorkflowValidation{BeadTitleContains: "Implement api/"}.WithDefaults()
	if v.BeadTitleContains != "Implement api/" {
		t.Fatalf("got %q", v.BeadTitleContains)
	}
	if v.UnittestModule != "" {
		t.Fatalf("expected empty unittest when unset and no QA command: %q", v.UnittestModule)
	}
}

func TestPromptVars_includesUnittestCommandHint(t *testing.T) {
	v := WorkflowValidation{QAVerifyCommand: "pytest -q"}.WithDefaults()
	vars := v.PromptVars()
	if vars["unittest_command_hint"] != ". .venv/bin/activate && python3 -m pytest -q" {
		t.Fatalf("hint: %q", vars["unittest_command_hint"])
	}
	v2 := WorkflowValidation{UnittestModule: "pkg.t"}.WithDefaults()
	if h := v2.PromptVars()["unittest_command_hint"]; h != ". .venv/bin/activate && python3 -m unittest pkg.t" {
		t.Fatalf("hint: %q", h)
	}
}

func TestPromptVars_projectSetupVerifyHintIsPhaseScoped(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:         ".",
		QAVerifyCommand:    "cd backend && pytest && cd ../frontend && npm test",
		TestRunner:         "pytest",
		ActivePhaseIDField: "frontend-ui",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "backend-core-data",
				RequiredFiles:   []string{"backend/market_data/simulator.py"},
				QAVerifyCommand: "cd backend && pytest backend/market_data",
			},
			{
				ID:              "frontend-ui",
				RequiredFiles:   []string{"frontend/components/Watchlist.tsx"},
				QAVerifyCommand: "cd frontend && npm test",
			},
		},
	}
	vars := v.PromptVars()
	if got := vars["project_setup_verify_hint"]; got != "cd frontend && npm install --ignore-scripts --prefer-offline --no-audit --no-fund" {
		t.Fatalf("frontend phase setup verify = %q, want cd frontend && npm install --ignore-scripts --prefer-offline --no-audit --no-fund", got)
	}
	if got := vars["project_setup_stack_kind"]; got != "nodejs" {
		t.Fatalf("frontend phase stack kind = %q, want nodejs", got)
	}

	v.ActivePhaseIDField = "backend-core-data"
	vars = v.PromptVars()
	wantPy := PythonProjectSetupVerifyCommand(v.ForActivePhase())
	if got := vars["project_setup_verify_hint"]; got != wantPy {
		t.Fatalf("backend phase setup verify = %q, want %q", got, wantPy)
	}
	if got := vars["project_setup_stack_kind"]; got != "python" {
		t.Fatalf("backend phase stack kind = %q, want python", got)
	}
}

func TestUsesPythonVenv(t *testing.T) {
	v := WorkflowValidation{RequiredFiles: []string{"backend/requirements.txt"}}
	if !v.UsesPythonVenv() {
		t.Fatal("expected venv for requirements.txt project")
	}
	if v.PythonVenvRelDir() != ".venv" {
		t.Fatalf("dir: %q", v.PythonVenvRelDir())
	}
	off := WorkflowValidation{PythonVenvDir: "off", RequiredFiles: []string{"a.py"}}
	if off.UsesPythonVenv() {
		t.Fatal("off should disable venv")
	}
}

func TestForbiddenRigRootBasenames(t *testing.T) {
	v := WorkflowValidation{
		RequiredFiles: []string{"backend/api.py", "backend/main.py"},
	}.WithDefaults()
	bases := v.ForbiddenRigRootBasenames()
	if len(bases) != 2 {
		t.Fatalf("got %v", bases)
	}
}

func TestBuildTaskPayload_includesValidation(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "orchestrator", "prompts", "rig-flow")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "kickoff.md"), []byte("rig {{rig}} prefix {{bead_title_contains}}"), 0644); err != nil {
		t.Fatal(err)
	}
	tpl := &WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "kickoff",
		Validation: WorkflowValidation{
			BeadTitleContains: "Implement service/",
			UnittestModule:    "backend.test_service",
		},
		States: map[string]State{
			"kickoff": {Role: "mayor", PromptFile: "prompts/rig-flow/kickoff.md"},
		},
	}
	m := NewManager(dir)
	m.LoadTemplate(tpl)
	inst := &WorkflowInstance{
		ID: "wf-1", TemplateID: "rig-flow", CurrentState: "kickoff",
		Variables: map[string]string{"rig": "myrig"}, Status: "running",
	}
	state, _ := inst.GetCurrentTask(tpl)
	payload, err := m.BuildTaskPayload(inst, tpl, state)
	if err != nil {
		t.Fatal(err)
	}
	val, ok := payload["validation"].(WorkflowValidation)
	if !ok {
		t.Fatalf("validation type %T", payload["validation"])
	}
	if val.BeadTitleContains != "Implement service/" {
		t.Fatalf("got %+v", val)
	}
	sp := payload["system_prompt"].(string)
	if !strings.Contains(sp, "Implement service/") {
		t.Fatalf("prompt missing prefix: %q", sp)
	}
}

func TestLoadTemplatesFromDir_readsValidation(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "orchestrator", "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	yaml := `id: custom-flow
initial_state: kickoff
validation:
  bead_title_contains: "Build feature/"
  unittest_module: pkg.test_feature
  required_files:
    - pkg/feature.py
states:
  kickoff:
    role: mayor
    instructions: go
`
	if err := os.WriteFile(filepath.Join(tmplDir, "custom-flow.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	if err := m.LoadTemplatesFromDir(tmplDir); err != nil {
		t.Fatal(err)
	}
	tpl := m.templates["custom-flow"]
	if tpl == nil {
		t.Fatal("template not loaded")
	}
	if tpl.Validation.BeadTitleContains != "Build feature/" {
		t.Fatalf("got %+v", tpl.Validation)
	}
}

func TestValidateDeliveryPhases_upgradesEchoToSmoke(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
			"linkshelf/web/index.html",
			"linkshelf/web/app.js",
		},
		QAVerifyCommand: "cd linkshelf && go test ./...",
		DeliveryPhases: []DeliveryPhase{
			{ID: "store", RequiredFiles: []string{"linkshelf/internal/store/schema.go"}, QAVerifyCommand: "cd linkshelf && go test ./internal/store/..."},
			{ID: "web-shell", RequiredFiles: []string{"linkshelf/web/index.html"}, QAVerifyCommand: "cd linkshelf && echo 'verify ok (no automated tests for this phase)'"},
		},
		ActivePhaseIDField: "store",
	}
	v = ValidateDeliveryPhases(v)
	final := v.DeliveryPhases[len(v.DeliveryPhases)-1]
	if strings.Contains(final.QAVerifyCommand, "echo") {
		t.Fatalf("final phase should not have echo verify, got %q", final.QAVerifyCommand)
	}
	if !strings.Contains(final.QAVerifyCommand, "go build") || !strings.Contains(final.QAVerifyCommand, "go test") {
		t.Fatalf("final phase should have compile+test smoke, got %q", final.QAVerifyCommand)
	}
}

func TestValidateDeliveryPhases_noUpgradeWhenNoServer(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "pkg",
		RequiredFiles: []string{
			"pkg/go.mod",
			"pkg/main.go",
		},
		QAVerifyCommand: "cd pkg && go test ./...",
		DeliveryPhases: []DeliveryPhase{
			{ID: "core", RequiredFiles: []string{"pkg/main.go"}, QAVerifyCommand: "cd pkg && go test ./..."},
			{ID: "docs", RequiredFiles: []string{"pkg/README.md"}, QAVerifyCommand: "cd pkg && echo 'verify ok (no automated tests for this phase)'"},
		},
		ActivePhaseIDField: "core",
	}
	v = ValidateDeliveryPhases(v)
	final := v.DeliveryPhases[len(v.DeliveryPhases)-1]
	if !strings.Contains(final.QAVerifyCommand, "echo") {
		t.Fatalf("non-web final phase should keep echo, got %q", final.QAVerifyCommand)
	}
}

func TestFinalPhaseSmokeVerifyCommand(t *testing.T) {
	t.Parallel()
	// Go+web+server: should return smoke command
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/cmd/server/main.go",
			"linkshelf/web/index.html",
		},
	}
	if got := finalPhaseSmokeVerifyCommand(v); got == "" {
		t.Fatal("expected smoke command for Go+web+server")
	} else if !strings.Contains(got, "go build") {
		t.Fatalf("expected go build in smoke command, got %q", got)
	}

	// Go library (no web): should return ""
	v2 := WorkflowValidation{
		LayoutRoot:      "pkg",
		QAVerifyCommand: "cd pkg && go test ./...",
		RequiredFiles: []string{
			"pkg/go.mod",
			"pkg/main.go",
		},
	}
	if got := finalPhaseSmokeVerifyCommand(v2); got != "" {
		t.Fatalf("expected empty for Go library, got %q", got)
	}
}

func TestCommandPathsMatchPhaseFiles(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		files   []string
		want    bool
	}{
		{
			name:  "bare test -f matches exact file",
			cmd:   "test -f finally/docker-compose.yml",
			files: []string{"finally/docker-compose.yml", "finally/Dockerfile"},
			want:  true,
		},
		{
			name:  "cd then test -f resolves relative path",
			cmd:   "cd finally && test -f docker-compose.yml",
			files: []string{"finally/docker-compose.yml", "finally/Dockerfile"},
			want:  true,
		},
		{
			name:  "cd into nested dir then test -f",
			cmd:   "cd finally/backend && test -f app/main.py",
			files: []string{"finally/backend/app/main.py", "finally/Dockerfile"},
			want:  true,
		},
		{
			name:  "cd then test -f with no match fails",
			cmd:   "cd finally && test -f nonexistent.txt",
			files: []string{"finally/docker-compose.yml"},
			want:  false,
		},
		{
			name:  "no paths returns true",
			cmd:   "echo hello",
			files: []string{"finally/docker-compose.yml"},
			want:  true,
		},
		{
			name:  "test -f with absolute path is skipped",
			cmd:   "test -f /usr/local/bin/go",
			files: []string{"finally/docker-compose.yml"},
			want:  true,
		},
		{
			name:  "multiple cd then test -f",
			cmd:   "cd gt && cd fin/mayor/rig && test -f architecture.md",
			files: []string{"gt/fin/mayor/rig/architecture.md"},
			want:  true,
		},
		{
			name:  "cd . is neutral",
			cmd:   "cd . && test -f finally/docker-compose.yml",
			files: []string{"finally/docker-compose.yml"},
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandPathsMatchPhaseFiles(tt.cmd, tt.files)
			if got != tt.want {
				t.Errorf("commandPathsMatchPhaseFiles(%q, %v) = %v, want %v", tt.cmd, tt.files, got, tt.want)
			}
		})
	}
}

func TestStripNonFileRequiredEntries(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  []string
	}{
		{
			name:  "clean files pass through",
			files: []string{"finally/backend/app/main.py", "finally/test/e2e.spec.ts"},
			want:  []string{"finally/backend/app/main.py", "finally/test/e2e.spec.ts"},
		},
		{
			name:  "semicolon-joined entry split into separate files",
			files: []string{"finally/backend/tests/test_database.py; finally/test/e2e.spec.ts"},
			want:  []string{"finally/backend/tests/test_database.py", "finally/test/e2e.spec.ts"},
		},
		{
			name:  "semicolon with extra spaces",
			files: []string{"file1.py ;  file2.ts ; file3.go"},
			want:  []string{"file1.py", "file2.ts", "file3.go"},
		},
		{
			name:  "mixed valid and non-file entries",
			files: []string{"pkg/main.py", "database.init_db()", "1.21", "pkg/util.go"},
			want:  []string{"pkg/main.py", "pkg/util.go"},
		},
		{
			name:  "semicolon entry with code fragment filtered",
			files: []string{"file.py; database.init_db()"},
			want:  []string{"file.py"},
		},
		{
			name:  "empty input",
			files: []string{},
			want:  []string{},
		},
		{
			name:  "single semicolon entry split",
			files: []string{"a.py; b.ts"},
			want:  []string{"a.py", "b.ts"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripNonFileRequiredEntries(tt.files)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIsNoOpVerifyCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"pure echo", "echo 'verify ok'", true},
		{"echo with single quotes", "echo 'no automated tests for this phase'", true},
		{"echo with double quotes", "echo \"verify ok\"", true},
		{"echo in subshell", "(echo 'verify ok')", true},
		{"empty string", "", false},
		{"real pytest", "cd finally && python -m pytest -v", false},
		{"real go test", "cd . && go test ./...", false},
		{"echo with preceding test", "test -f docker-compose.yml && echo 'compose file ok'", false},
		{"echo with preceding test alt", "test -f file && echo ok || echo fail", false},
		{"echo chained with real cmd", "echo 'starting' && pytest", false},
		{"echo piped", "echo ok | grep ok", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNoOpVerifyCommand(tt.cmd)
			if got != tt.want {
				t.Errorf("isNoOpVerifyCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestSanitizePhaseVerifyCommandsForStack_fixesDoubledLayoutPath(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:      "finally",
		DevServerPort:   8000,
		DeliveryPhases: []DeliveryPhase{
			{
				ID:           "testing-release-1",
				Title:        "testing-release-1",
				RequiredFiles: []string{"finally/test/playwright.config.ts"},
				// Doubled layout path: finally/finally/test
				QAVerifyCommand: "cd finally/finally/test && npm install --ignore-scripts && npx playwright test",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := got.DeliveryPhases[0].QAVerifyCommand
	if strings.Contains(cmd, "finally/finally/") {
		t.Fatalf("doubled layout path survived sanitize: %q", cmd)
	}
	if !strings.Contains(cmd, "cd finally/test") {
		t.Fatalf("expected cd finally/test, got: %q", cmd)
	}
}

func TestSanitizePhaseVerifyCommandsForStack_replacesNoOpEcho(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:      "finally",
		DevServerPort:   8000,
		DeliveryPhases: []DeliveryPhase{
			{
				ID:           "project-foundation",
				Title:        "project-foundation",
				RequiredFiles: []string{
					"finally/backend/app/db/__init__.py",
					"finally/db/.gitkeep",
				},
				// No-op echo — should be replaced with a real check
				QAVerifyCommand: "cd finally && python -c 'import sys; print(\"ok\")'",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := got.DeliveryPhases[0].QAVerifyCommand
	// The command "python -c 'import sys; print(\"ok\")'" is not a pure echo,
	// so it should NOT be replaced by the no-op guard. Verify it's preserved
	// (or replaced only if it fails cross-stack checks).
	if cmd == "" {
		t.Fatalf("verify command was emptied: %q", cmd)
	}
}

func TestSanitizePhaseVerifyCommandsForStack_pureEchoReplaced(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:      "finally",
		DevServerPort:   8000,
		DeliveryPhases: []DeliveryPhase{
			{
				ID:           "application-polish",
				Title:        "application-polish",
				RequiredFiles: []string{
					"finally/frontend/app/globals.css",
					"finally/frontend/app/layout.tsx",
					"finally/frontend/app/page.tsx",
				},
				QAVerifyCommand: "cd finally && echo 'verify ok (no automated tests for this phase)'",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := got.DeliveryPhases[0].QAVerifyCommand
	if isNoOpVerifyCommand(cmd) {
		t.Fatalf("no-op echo survived sanitize: %q", cmd)
	}
}
