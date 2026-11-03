package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestScaffoldHybridHonorsSPECWithSparseProfile(t *testing.T) {
	for _, suppliedPlan := range []bool{false, true} {
		name := "no LLM plan"
		if suppliedPlan {
			name = "incorrect LLM plan"
		}
		t.Run(name, func(t *testing.T) {
			town := t.TempDir()
			rig := "workbench"
			rigDir := filepath.Join(town, rig, "mayor", "rig")
			if err := os.MkdirAll(filepath.Join(rigDir, ".gastown"), 0755); err != nil {
				t.Fatal(err)
			}
			// Deliberately omit frontend files and manifests, reproducing a sparse
			// spec-index profile. SPEC must still drive the hybrid scaffold.
			v := WorkflowValidation{
				LayoutRoot: "product", DevServerPort: 8123, TestRunner: "pytest",
				RequiredFiles:  []string{"product/backend/app/main.py", "product/Dockerfile", "product/test/docker-compose.test.yml", "product/test/playwright.config.ts"},
				DeliveryPhases: []DeliveryPhase{{ID: "release", RequiredFiles: []string{"product/test/docker-compose.test.yml", "product/test/playwright.config.ts"}}},
			}
			profile, err := json.Marshal(rigProfileEnvelope{Version: 1, Validation: v})
			if err != nil {
				t.Fatal(err)
			}
			profilePath := filepath.Join(rigDir, ".gastown", "workflow-profile.json")
			if err := os.WriteFile(profilePath, profile, 0644); err != nil {
				t.Fatal(err)
			}
			spec := `# Workbench
Frontend: Next.js static export.
Backend: FastAPI, managed as a uv project with backend/pyproject.toml and backend/uv.lock.
FROM node:22-slim AS frontend
FROM python:3.12-slim AS runtime
The Dockerfile must COPY --from=frontend /frontend/out /app/frontend/out.
  - CMD: uvicorn app.main:app --host 0.0.0.0 --port 8123
Healthcheck: curl -f http://localhost:8123/api/health
`
			if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
				t.Fatal(err)
			}
			var plan *ScaffoldPlan
			if suppliedPlan {
				plan = &ScaffoldPlan{Kind: "single-container", Stack: "go", LayoutRoot: "wrong-layout", Port: 9999}
			}
			if _, err := ScaffoldRigIntegrationTemplates(town, rig, plan); err != nil {
				t.Fatal(err)
			}
			dfPath := filepath.Join(rigDir, "product", "Dockerfile")
			df, err := os.ReadFile(dfPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"FROM node:22-slim AS frontend", "WORKDIR /frontend",
				"FROM python:3.12-slim AS runtime", "WORKDIR /app/backend",
				"COPY backend/ ./", "uv sync --frozen --no-dev --no-install-project",
				`PATH="/app/backend/.venv/bin:$PATH"`,
				"COPY --from=frontend /frontend/out /app/frontend/out", "EXPOSE 8123",
			} {
				if !strings.Contains(string(df), want) {
					t.Errorf("rendered Dockerfile missing %q:\n%s", want, df)
				}
			}
			for _, forbidden := range []string{"golang:", "./static/", "python app.py", "2>/dev/null", "{{"} {
				if strings.Contains(string(df), forbidden) {
					t.Errorf("rendered Dockerfile contains stale fallback %q:\n%s", forbidden, df)
				}
			}
			wantCMD := []string{"uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8123"}
			cmdJSON := strings.TrimPrefix(strings.Split(strings.TrimSpace(string(df)), "\nCMD ")[1], "CMD ")
			var gotCMD []string
			if err := json.Unmarshal([]byte(cmdJSON), &gotCMD); err != nil {
				t.Fatal(err)
			}
			if strings.Join(gotCMD, "\x00") != strings.Join(wantCMD, "\x00") {
				t.Errorf("CMD=%v, want SPEC command %v", gotCMD, wantCMD)
			}
			compose, err := os.ReadFile(filepath.Join(rigDir, "product", "test", "docker-compose.test.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				Services map[string]struct {
					Healthcheck struct{ Test []string }
				}
			}
			if err := yaml.Unmarshal(compose, &parsed); err != nil {
				t.Fatal(err)
			}
			health := parsed.Services["app"].Healthcheck.Test
			if strings.Join(health, " ") != "CMD curl -f http://localhost:8123/api/health" {
				t.Errorf("healthcheck=%v, want SPEC readiness endpoint", health)
			}
			// Repeated scaffolding must preserve an agent/user amendment.
			custom := append(df, []byte("\n# application-specific amendment\n")...)
			if err := os.WriteFile(dfPath, custom, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := ScaffoldRigIntegrationTemplates(town, rig, plan); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(dfPath)
			if string(got) != string(custom) {
				t.Fatal("scaffolding overwrote an existing Dockerfile amendment")
			}
			gotProfile, _ := os.ReadFile(profilePath)
			if string(gotProfile) != string(profile) {
				t.Fatal("scaffolding changed the workflow profile")
			}
		})
	}
}

func TestPythonScaffoldManifestAndEntrypointPaths(t *testing.T) {
	for _, tt := range []struct {
		name    string
		files   []string
		install string
		venv    string
		module  string
		appDir  string
	}{
		{"root requirements", []string{"service/requirements.txt", "service/main.py"}, "python -m pip install --no-cache-dir -r requirements.txt", "/app/.venv/bin", "main:app", ""},
		{"root requirements nested app", []string{"service/requirements.txt", "service/backend/app/main.py"}, "python -m pip install --no-cache-dir -r requirements.txt", "/app/.venv/bin", "app.main:app", "backend"},
		{"nested uv project", []string{"service/api/pyproject.toml", "service/api/uv.lock", "service/api/app/main.py"}, "cd 'api' && python -m pip install --no-cache-dir uv && uv sync --frozen --no-dev --no-install-project", "/app/api/.venv/bin", "app.main:app", "api"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := WorkflowValidation{LayoutRoot: "service", RequiredFiles: tt.files}
			vals := buildTemplateValues(&ScaffoldPlan{Stack: "python"}, 8765, "single-container", &v)
			if vals["PY_INSTALL"] != tt.install || vals["PY_VENV_BIN"] != tt.venv {
				t.Fatalf("inconsistent installation/venv: %s / %s", vals["PY_INSTALL"], vals["PY_VENV_BIN"])
			}
			var args []string
			if err := json.Unmarshal([]byte(vals["RUN_CMD"]), &args); err != nil {
				t.Fatal(err)
			}
			if len(args) < 4 || args[3] != tt.module {
				t.Fatalf("wrong Python module: %v", args)
			}
			if tt.appDir != "" && !strings.Contains(vals["RUN_CMD"], `"--app-dir","`+tt.appDir+`"`) {
				t.Fatalf("missing app import directory: %v", args)
			}
		})
	}
}

func TestScaffoldCustomExportAndProjectDirectories(t *testing.T) {
	v := WorkflowValidation{LayoutRoot: "service", RequiredFiles: []string{
		"service/api/pyproject.toml", "service/api/uv.lock", "service/api/app/main.py", "service/ui/package.json",
	}}
	vals := buildTemplateValues(&ScaffoldPlan{Stack: "hybrid"}, 8088, "single-container", &v)
	applyScaffoldSpecValues(vals, "COPY --from=ui_build /ui/dist /srv/assets\nFROM python:3.12-alpine", 8088)
	data, err := townAssets.ReadFile(rigInitTemplateDir + "/Dockerfile.hybrid")
	if err != nil {
		t.Fatal(err)
	}
	df := renderTemplate(string(data), vals)
	for _, want := range []string{"AS ui_build", "WORKDIR /ui", "COPY ui/package*.json", "WORKDIR /app/api", "COPY api/ ./", "COPY --from=ui_build /ui/dist /srv/assets", "RUN apk add --no-cache curl"} {
		if !strings.Contains(df, want) {
			t.Errorf("custom SPEC contract missing %q:\n%s", want, df)
		}
	}
}

func TestScaffoldBrowserToolingDoesNotImplyHybridSPEC(t *testing.T) {
	v := WorkflowValidation{LayoutRoot: "api", TestRunner: "pytest", RequiredFiles: []string{"api/main.py", "api/test/package.json", "api/test/playwright.config.ts"}}
	plan := scaffoldPlanForSpec(nil, &v, "Backend: FastAPI. Browser tests: Playwright with TypeScript; no frontend build.")
	if plan.Stack != "python" {
		t.Fatalf("browser tooling incorrectly selected %q", plan.Stack)
	}
}

func TestScaffoldRootComposeBuildsDeclaredApplicationDockerfile(t *testing.T) {
	v := WorkflowValidation{LayoutRoot: "product", RequiredFiles: []string{"product/Dockerfile", "product/docker-compose.yml"}}
	plan := scaffoldPlanForSpec(nil, &v, "Single Docker container. Frontend: Next.js static export. Backend: FastAPI.")
	kind, dst := resolveComposeKind(plan, &v)
	templates := selectTemplatesForPlan(plan, &v)
	if kind != "single-container" || dst != "docker-compose.yml" || templates["Dockerfile.hybrid"] != "Dockerfile" {
		t.Fatalf("production Dockerfile was confused with the browser runner: kind=%s dst=%s templates=%v", kind, dst, templates)
	}
	if templates["Dockerfile.playwright-stage"] != "Dockerfile.playwright" {
		t.Fatalf("browser runner must have a separate Dockerfile: %v", templates)
	}
}

func TestScaffoldRuntimeTemplatesDoNotRequireAbsentOptionalFiles(t *testing.T) {
	for _, tt := range []struct{ stack, template, absent string }{
		{"go", "Dockerfile", "COPY go.mod go.sum"},
		{"node", "Dockerfile.node", "COPY --from=builder /build/public"},
	} {
		t.Run(tt.stack, func(t *testing.T) {
			v := WorkflowValidation{LayoutRoot: "app", RequiredFiles: []string{"app/main.go"}}
			vals := buildTemplateValues(&ScaffoldPlan{Stack: tt.stack}, 8765, "single-container", &v)
			data, err := townAssets.ReadFile(rigInitTemplateDir + "/" + tt.template)
			if err != nil {
				t.Fatal(err)
			}
			df := renderTemplate(string(data), vals)
			if strings.Contains(df, tt.absent) || strings.Contains(df, "{{") || !strings.Contains(df, "EXPOSE 8765") {
				t.Fatalf("unusable %s runtime template:\n%s", tt.stack, df)
			}
			if tt.stack == "go" && (!strings.Contains(df, "go build -o /server .") || !strings.Contains(df, `":8765"`)) {
				t.Fatalf("Go entrypoint/port disagree with profile:\n%s", df)
			}
			if tt.stack == "go" && !strings.Contains(df, "COPY --from=builder /app/web ./web") {
				t.Fatalf("optional web directory must come from the prepared builder filesystem:\n%s", df)
			}
		})
	}
}
