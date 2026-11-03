package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclaredSpecPhaseHeadings(t *testing.T) {
	spec := "# Product\n# Phase 1 — Foundation\n## Phase 2: Live UI\n### Phase 3 - Polish\n```markdown\n# Phase 4 — Example only\n```\n# Phase 3 - Polish\n"
	phases := DeclaredSpecPhaseHeadings(spec)
	if len(phases) != 3 || phases[1].ID != "live-ui" || phases[2].ID != "polish" {
		t.Fatalf("unexpected declared phases: %+v", phases)
	}
}

func TestProfileSyncPreservesScopeAndSupplementalLayouts(t *testing.T) {
	town := t.TempDir()
	rig := "scope-test"
	dir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(dir, rigProfileDir), 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# Product\n## Layout\n```\nproduct/\n└── server.py\n```\n# Phase 1 — Service\n# Phase 2 — Client\n# Phase 3 — Polish\n"
	arch := "# Architecture\n## Planned file layout\n- `product/server.py`\n## Delivery phases\n| Phase | Deliverables |\n|---|---|\n| service | `product/server.py` |\n## Supplemental client file layout\nRuntime output is `product/client/dist/index.html` (generated).\n- `product/client/package.json`\n- `product/client/vite.config.ts`\n- `product/client/src/view.tsx`\n## Examples\nDo not create `product/example.js`.\n"
	for name, text := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{LayoutRoot: "product", RequiredFiles: []string{"product/server.py"}, DeliveryPhases: []DeliveryPhase{
		{ID: "service", RequiredFiles: []string{"product/server.py"}, QAVerifyCommand: "cd product && python -m compileall server.py"},
		{ID: "client", RequiredFiles: []string{"product/client/src/view.tsx"}, QAVerifyCommand: "cd product/client && npm run build"},
		{ID: "polish", RequiredFiles: []string{"product/client/src/view.tsx"}, QAVerifyCommand: "cd product/client && npm run build", DependsOn: []string{"client"}},
	}}
	if defect := ValidateRigWorkflowProfileForQA(town, rig, v); !strings.Contains(defect, "declared deliverable \"product/client/package.json\" is missing") {
		t.Fatalf("missing build inputs accepted: %s", defect)
	}
	raw, err := marshalRigProfileJSON(rigProfileEnvelope{Version: 1, Validation: v})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rigProfileDir, rigProfileFile), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if changed, err := SyncRigWorkflowProfileFromArchitecture(town, rig); err != nil || !changed {
		t.Fatalf("sync: changed=%v err=%v", changed, err)
	}
	got, _, err := LoadRawRigWorkflowProfile(town, rig)
	if err != nil {
		t.Fatal(err)
	}
	if missing := missingDeclaredPhases(DeclaredSpecPhaseHeadings(spec), got.DeliveryPhases); len(missing) != 0 {
		t.Fatalf("sync contracted SPEC scope: %v", missing)
	}
	for _, f := range []string{"product/client/package.json", "product/client/vite.config.ts", "product/client/src/view.tsx"} {
		if !containsString(got.UnionRequiredFiles(), f) {
			t.Errorf("supplemental deliverable lost: %s", f)
		}
	}
	for _, f := range []string{"product/client/dist/index.html", "product/example.js"} {
		if containsString(got.UnionRequiredFiles(), f) {
			t.Errorf("non-deliverable included: %s", f)
		}
	}
	// Design QA must also catch an already-contracted profile.
	got.DeliveryPhases = got.DeliveryPhases[:1]
	defect := ValidateRigWorkflowProfileForQA(town, rig, got)
	if !strings.Contains(defect, "SPEC delivery phase \"Client\" is missing") || !strings.Contains(defect, "SPEC delivery phase \"Polish\" is missing") {
		t.Fatalf("scope contraction accepted: %s", defect)
	}
}

func TestNodeBuildGateProblems(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		wantProblem   bool
	}{
		{"backend-only", "cd workspace/api && python -c 'import app'", true},
		{"typecheck-only", "cd workspace/client && npx tsc --noEmit", true},
		{"test-enumeration", "cd workspace/client && npx playwright test --list", true},
		{"wrong-project", "cd workspace/api && npm run build", true},
		{"echo-is-not-build", "echo 'npm run build'", true},
		{"npm", "cd workspace/client && npm run build", false},
		{"pnpm", "cd workspace/client && pnpm build", false},
		{"yarn", "cd workspace/client && yarn build", false},
		{"container", "cd workspace && docker compose build", false},
		{"container-with-options", "cd workspace && docker compose -f compose.test.yml build --no-cache", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := WorkflowValidation{RequiredFiles: []string{"workspace/client/package.json", "workspace/client/vite.config.ts"}, DeliveryPhases: []DeliveryPhase{{ID: "client", RequiredFiles: []string{"workspace/client/src/screen.tsx", "workspace/api/app.py"}, QAVerifyCommand: tc.command}}}
			problems := nodeBuildGateProblems(v)
			if (len(problems) > 0) != tc.wantProblem {
				t.Fatalf("problems=%v wantProblem=%v", problems, tc.wantProblem)
			}
		})
	}
	t.Run("static-assets", func(t *testing.T) {
		v := WorkflowValidation{DeliveryPhases: []DeliveryPhase{{ID: "web", RequiredFiles: []string{"site/web/app.js", "site/web/index.html"}, QAVerifyCommand: "go test ./..."}}}
		if p := nodeBuildGateProblems(v); len(p) != 0 {
			t.Fatalf("plain static assets require no Node build: %v", p)
		}
	})
}
