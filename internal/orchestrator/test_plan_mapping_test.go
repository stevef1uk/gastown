package orchestrator

import "testing"

func TestPhaseRequiresTestFileAcrossSupportedStacks(t *testing.T) {
	for _, path := range []string{"app/handler_test.go", "tests/test_service.py", "service_test.py", "view.test.tsx", "view.spec.js", "view.test.jsx"} {
		t.Run(path, func(t *testing.T) {
			if !PhaseRequiresTestFile(DeliveryPhase{RequiredFiles: []string{path}}) {
				t.Fatalf("declared test file %s must require a test mapping", path)
			}
		})
	}
}

func TestTestPlanFileMappingsAcrossProjectLayouts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		v         WorkflowValidation
		blocks    []TestPlanBlock
		wantError bool
	}{
		{
			name:   "unphased Python root layout",
			v:      WorkflowValidation{LayoutRoot: ".", RequiredFiles: []string{"service.py", "tests/test_service.py"}},
			blocks: []TestPlanBlock{{ReqID: "GET /items", TestFile: "tests/test_service.py"}},
		},
		{
			name:      "unlisted companion is rejected",
			v:         WorkflowValidation{RequiredFiles: []string{"service.py"}},
			blocks:    []TestPlanBlock{{ReqID: "GET /items", TestFile: "tests/test_service.py"}},
			wantError: true,
		},
		{
			name:   "mixed Go backend and frontend tests",
			v:      WorkflowValidation{RequiredFiles: []string{"backend/handler_test.go", "frontend/view.spec.ts"}},
			blocks: []TestPlanBlock{{ReqID: "backend", TestFile: "backend/handler_test.go"}, {ReqID: "frontend", TestFile: "frontend/view.spec.ts"}},
		},
		{
			name:   "manifest phase has no test file",
			v:      WorkflowValidation{DeliveryPhases: []DeliveryPhase{{ID: "bootstrap", RequiredFiles: []string{"app/requirements.txt"}}}},
			blocks: []TestPlanBlock{{ReqID: "bootstrap", Phase: "bootstrap"}},
		},
		{
			name:      "future-phase test cannot be assigned to bootstrap",
			v:         WorkflowValidation{DeliveryPhases: []DeliveryPhase{{ID: "bootstrap", RequiredFiles: []string{"app/requirements.txt"}}, {ID: "checks", RequiredFiles: []string{"app/tests/test_service.py"}}}},
			blocks:    []TestPlanBlock{{ReqID: "bootstrap", Phase: "bootstrap", TestFile: "app/tests/test_service.py"}},
			wantError: true,
		},
		{
			name:      "unknown phase rejected even with empty test path",
			v:         WorkflowValidation{DeliveryPhases: []DeliveryPhase{{ID: "bootstrap"}}},
			blocks:    []TestPlanBlock{{ReqID: "bootstrap", Phase: "obsolete"}},
			wantError: true,
		},
		{
			name:      "case-sensitive file names",
			v:         WorkflowValidation{RequiredFiles: []string{"tests/test_service.py"}},
			blocks:    []TestPlanBlock{{TestFile: "tests/Test_Service.py"}},
			wantError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTestPlanFileMappings(tc.v, tc.blocks); (err != nil) != tc.wantError {
				t.Fatalf("want error=%v, got %v", tc.wantError, err)
			}
		})
	}
}
