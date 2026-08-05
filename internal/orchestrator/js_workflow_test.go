package orchestrator

import "testing"

func TestNodeProjectSetupVerifyCommand(t *testing.T) {
	cases := []struct {
		name string
		v    WorkflowValidation
		want string
	}{
		{
			name: "frontend subdirectory — preserves cd prefix",
			v:    WorkflowValidation{QAVerifyCommand: "cd frontend && npm test"},
			want: "cd frontend && npm install --ignore-scripts",
		},
		{
			name: "root level npm",
			v:    WorkflowValidation{QAVerifyCommand: "npm test"},
			want: "npm install --ignore-scripts",
		},
		{
			name: "app layout — preserves cd prefix",
			v:    WorkflowValidation{QAVerifyCommand: "cd app && npm test"},
			want: "cd app && npm install --ignore-scripts",
		},
		{
			name: "no fallback to required files directory",
			v: WorkflowValidation{
				QAVerifyCommand: "npm test",
				RequiredFiles:   []string{"frontend/components/Watchlist.tsx", "frontend/app/page.tsx"},
			},
			want: "npm install --ignore-scripts",
		},
		{
			name: "no cd when node files are at root",
			v: WorkflowValidation{
				QAVerifyCommand: "npm test",
				RequiredFiles:   []string{"app.tsx", "backend/main.py"},
			},
			want: "npm install --ignore-scripts",
		},
		{
			name: "cd prefix preservation with multiple ands",
			v:    WorkflowValidation{QAVerifyCommand: "cd frontend && npm install && npx tsc --noEmit && npm test"},
			want: "cd frontend && npm install --ignore-scripts",
		},
		{
			name: "yarn in subdirectory — preserves cd",
			v:    WorkflowValidation{QAVerifyCommand: "cd app && yarn test"},
			want: "cd app && yarn install --ignore-scripts",
		},
		{
			name: "pnpm in subdirectory — preserves cd",
			v:    WorkflowValidation{QAVerifyCommand: "cd frontend && pnpm test"},
			want: "cd frontend && pnpm install --ignore-scripts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NodeProjectSetupVerifyCommand(tc.v)
			if got != tc.want {
				t.Errorf("NodeProjectSetupVerifyCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestProjectSetupStackKindPerPhase verifies that dual-stack rigs scope
// project_setup to the active delivery phase's language.
func TestProjectSetupStackKindPerPhase(t *testing.T) {
	base := WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement finally/",
		QAVerifyCommand:   "cd backend && pytest && cd ../frontend && npm test",
		TestRunner:        "pytest",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "backend-core-data",
				Title:           "Backend Core",
				RequiredFiles:   []string{"backend/market_data/simulator.py"},
				QAVerifyCommand: "cd backend && pytest backend/market_data",
			},
			{
				ID:              "frontend-ui",
				Title:           "Frontend",
				RequiredFiles:   []string{"frontend/components/Watchlist.tsx"},
				QAVerifyCommand: "cd frontend && npm install && npx tsc --noEmit && npm test",
			},
		},
	}

	pythonV := base
	pythonV.ActivePhaseIDField = "backend-core-data"
	pythonScoped := pythonV.ForActivePhase()
	if got := ProjectSetupStackKind(pythonScoped); got != "python" {
		t.Errorf("backend phase stack = %q, want python", got)
	}
	if got := pythonScoped.ProjectSetupVerifyHint(); got != PythonProjectSetupVerifyCommand(pythonScoped) {
		t.Errorf("backend setup verify = %q, want python verify", got)
	}

	nodeV := base
	nodeV.ActivePhaseIDField = "frontend-ui"
	nodeScoped := nodeV.ForActivePhase()
	if got := ProjectSetupStackKind(nodeScoped); got != "nodejs" {
		t.Errorf("frontend phase stack = %q, want nodejs", got)
	}
	wantNode := "cd frontend && npm install --ignore-scripts"
	if got := nodeScoped.ProjectSetupVerifyHint(); got != wantNode {
		t.Errorf("frontend setup verify = %q, want %q", got, wantNode)
	}
}

// TestProjectSetupStackKindMultiLanguageGlobal verifies the global profile
// (which may mix languages in qa_verify_command) does not mis-detect as
// nodejs when the active phase is Python.
func TestProjectSetupStackKindMultiLanguageGlobal(t *testing.T) {
	v := WorkflowValidation{
		QAVerifyCommand: "cd backend && pytest && cd ../frontend && npm test",
		TestRunner:      "pytest",
		ActivePhaseIDField: "backend-core-data",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "backend-core-data",
				RequiredFiles:   []string{"backend/market_data/simulator.py"},
				QAVerifyCommand: "cd backend && pytest backend/market_data",
			},
		},
	}
	scoped := v.ForActivePhase()
	if got := ProjectSetupStackKind(scoped); got != "python" {
		t.Errorf("active phase is Python but stack = %q, want python", got)
	}
}

// TestNodeProjectSetupVerifyCommand_noNodeFiles verifies we don't mis-detect
// non-Node rigs as Node.js.
func TestWorkflowUsesNodeJS(t *testing.T) {
	cases := []struct {
		name string
		v    WorkflowValidation
		want bool
	}{
		{
			name: "tsx required files",
			v:    WorkflowValidation{RequiredFiles: []string{"frontend/app.tsx"}},
			want: true,
		},
		{
			name: "npm in qa command",
			v:    WorkflowValidation{QAVerifyCommand: "cd frontend && npm test"},
			want: true,
		},
		{
			name: "python only",
			v:    WorkflowValidation{RequiredFiles: []string{"backend/main.py"}, QAVerifyCommand: "pytest"},
			want: false,
		},
		{
			name: "go only",
			v:    WorkflowValidation{RequiredFiles: []string{"cmd/server/main.go"}, QAVerifyCommand: "go test ./..."},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkflowUsesNodeJS(tc.v); got != tc.want {
				t.Errorf("WorkflowUsesNodeJS() = %v, want %v", got, tc.want)
			}
		})
	}
}
