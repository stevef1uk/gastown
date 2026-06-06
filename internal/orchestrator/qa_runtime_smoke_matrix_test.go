package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeQARigDocs writes architecture.md (and optional SPEC.md) under mayor/rig for matrix tests.
func writeQARigDocs(t *testing.T, rigDir, archBody, specBody string) {
	t.Helper()
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if archBody != "" {
		if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(archBody), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if specBody != "" {
		if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(specBody), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeQARigWebIndex(t *testing.T, rigDir, layout string) {
	t.Helper()
	webDir := filepath.Join(rigDir, filepath.FromSlash(layout), "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	html := `<!DOCTYPE html><html><head><script src="/static/app.js"></script></head><body></body></html>`
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "app.js"), []byte("console.log('ok');\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestAPISmokeHasHTTPAPI_matrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec APISmokeSpec
		want bool
	}{
		{"empty", APISmokeSpec{}, false},
		{"root_only", APISmokeSpec{GETPaths: []string{"/"}}, false},
		{"api_get", APISmokeSpec{GETPaths: []string{"/api/links"}}, true},
		{"param_get", APISmokeSpec{GETPaths: []string{"/api/links/{id}"}}, true},
		{"post_only", APISmokeSpec{POSTProbes: []POSTSmokeProbe{{Path: "/api/items"}}}, true},
		{"static_asset_path", APISmokeSpec{GETPaths: []string{"/static/app.js"}}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := APISmokeHasHTTPAPI(tc.spec); got != tc.want {
				t.Fatalf("APISmokeHasHTTPAPI(%+v)=%v want %v", tc.spec, got, tc.want)
			}
		})
	}
}

func writeMatrixRigArch(t *testing.T, dir, rig, arch string) {
	t.Helper()
	if strings.TrimSpace(arch) == "" {
		return
	}
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowNeedsRuntimeSmoke_matrix(t *testing.T) {
	t.Parallel()
	const archAPI = `| GET | /api/links | JSON array |
| POST | /api/links | create |
`
	cases := []struct {
		name          string
		v             WorkflowValidation
		arch          string
		wantImplSmoke bool
	}{
		{
			name: "go_web_server",
			v: WorkflowValidation{
				QAVerifyCommand: "cd app && go test ./...",
				RequiredFiles:   []string{"app/web/index.html", "app/cmd/server/main.go"},
			},
			wantImplSmoke: true,
		},
		{
			name: "go_server_only",
			v: WorkflowValidation{
				QAVerifyCommand: "cd app && go test ./...",
				RequiredFiles:   []string{"app/cmd/server/main.go"},
			},
			wantImplSmoke: false,
		},
		{
			name: "go_library",
			v: WorkflowValidation{
				QAVerifyCommand: "cd app && go test ./...",
				RequiredFiles:   []string{"app/internal/store/store.go"},
			},
			wantImplSmoke: false,
		},
		{
			name: "python_backend_no_http_docs",
			v: WorkflowValidation{
				QAVerifyCommand: "python3 -m pytest -q",
				RequiredFiles:   []string{"backend/app.py"},
				PythonVenvDir:   ".venv",
			},
			wantImplSmoke: false,
		},
		{
			name: "python_with_api",
			v: WorkflowValidation{
				LayoutRoot:      "backend",
				QAVerifyCommand: "python3 -m pytest -q",
				RequiredFiles:   []string{"backend/app.py"},
				PythonVenvDir:   ".venv",
			},
			arch:          archAPI,
			wantImplSmoke: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			rig := "rig"
			writeMatrixRigArch(t, dir, rig, tc.arch)
			got := WorkflowNeedsRuntimeSmoke(dir, rig, tc.v)
			if got != tc.wantImplSmoke {
				t.Fatalf("WorkflowNeedsRuntimeSmoke=%v want %v", got, tc.wantImplSmoke)
			}
		})
	}
}

func TestWorkflowNeedsQARuntimeSmoke_matrix(t *testing.T) {
	t.Parallel()
	const (
		archAPI = `| GET | /api/links | JSON array |
| POST | /api/links | create |
`
		archStatic = `| GET | / | index.html |
`
		archEmpty = ""
	)

	cases := []struct {
		name     string
		v        WorkflowValidation
		arch     string
		spec     string
		writeWeb bool
		wantQA   bool
		wantAPI  bool
	}{
		{
			name: "go_library_no_http",
			v: WorkflowValidation{
				LayoutRoot:      "pkg",
				QAVerifyCommand: "cd pkg && go test ./...",
				RequiredFiles:   []string{"pkg/internal/core/core.go"},
			},
			arch:   archEmpty,
			wantQA: false,
		},
		{
			name: "go_web_static_only",
			v: WorkflowValidation{
				LayoutRoot:      "app",
				QAVerifyCommand: "cd app && go test ./...",
				RequiredFiles:   []string{"app/web/index.html", "app/cmd/server/main.go"},
			},
			arch:     archStatic,
			writeWeb: true,
			wantQA:   true,
			wantAPI:  false,
		},
		{
			name: "go_web_with_api",
			v: WorkflowValidation{
				LayoutRoot:      "linkshelf",
				QAVerifyCommand: "cd linkshelf && go test ./...",
				RequiredFiles: []string{
					"linkshelf/web/index.html",
					"linkshelf/cmd/server/main.go",
					"linkshelf/internal/api/handlers.go",
				},
			},
			arch:     archAPI,
			writeWeb: true,
			wantQA:   true,
			wantAPI:  true,
		},
		{
			name: "go_server_no_web_even_with_api_docs",
			v: WorkflowValidation{
				LayoutRoot:      "svc",
				QAVerifyCommand: "cd svc && go test ./...",
				RequiredFiles:   []string{"svc/cmd/server/main.go"},
			},
			arch:    archAPI,
			wantQA:  false,
			wantAPI: true, // docs have API; profile shape still skips QA smoke
		},
		{
			name: "python_pytest_only",
			v: WorkflowValidation{
				LayoutRoot:      "backend",
				QAVerifyCommand:   "python3 -m pytest -q",
				RequiredFiles:     []string{"backend/fizzbuzz.py"},
				PythonVenvDir:     ".venv",
			},
			arch:   archEmpty,
			wantQA: false,
		},
		{
			name: "python_with_api_and_app",
			v: WorkflowValidation{
				LayoutRoot:      "backend",
				QAVerifyCommand:   "python3 -m pytest -q",
				RequiredFiles:     []string{"backend/app.py", "backend/api/routes.py"},
			},
			arch:   archAPI,
			wantQA: true,
			wantAPI: true,
		},
		{
			name: "python_api_docs_no_server_file",
			v: WorkflowValidation{
				QAVerifyCommand: "python3 -m pytest -q",
				RequiredFiles:   []string{"backend/lib.py"},
			},
			arch:    archAPI,
			wantQA:  false,
			wantAPI: true,
		},
		{
			name: "go_web_no_docs",
			v: WorkflowValidation{
				QAVerifyCommand: "cd app && go test ./...",
				RequiredFiles:   []string{"app/web/index.html", "app/cmd/server/main.go"},
			},
			arch:     archEmpty,
			writeWeb: true,
			wantQA:   false, // no HTTP table in docs — unittest only
		},
		{
			name: "go_phased_backend_core_skips_smoke",
			v: WorkflowValidation{
				LayoutRoot:         "linkshelf",
				ActivePhaseIDField: "backend-core",
				QAVerifyCommand:    "cd linkshelf && go test ./...",
				RequiredFiles: []string{
					"linkshelf/go.mod",
					"linkshelf/internal/store/schema.go",
					"linkshelf/internal/store/store.go",
					"linkshelf/internal/api/handlers.go",
					"linkshelf/cmd/server/main.go",
					"linkshelf/web/index.html",
				},
				DeliveryPhases: []DeliveryPhase{
					{
						ID: "backend-core",
						RequiredFiles: []string{
							"linkshelf/go.mod",
							"linkshelf/internal/store/schema.go",
							"linkshelf/internal/store/store.go",
						},
						QAVerifyCommand: "cd linkshelf && go test ./internal/store",
					},
					{
						ID: "server-setup",
						RequiredFiles: []string{
							"linkshelf/cmd/server/main.go",
							"linkshelf/web/index.html",
						},
					},
				},
			},
			arch:    archAPI,
			wantQA:  false,
			wantAPI: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			rig := "rig"
			rigDir := filepath.Join(dir, rig, "mayor", "rig")
			writeQARigDocs(t, rigDir, tc.arch, tc.spec)
			if tc.writeWeb {
				layout := strings.Trim(strings.TrimSpace(tc.v.LayoutRoot), "/")
				if layout == "" {
					layout = "app"
				}
				writeQARigWebIndex(t, rigDir, layout)
			}
			got := WorkflowNeedsQARuntimeSmoke(dir, rig, tc.v)
			if got != tc.wantQA {
				t.Fatalf("WorkflowNeedsQARuntimeSmoke=%v want %v", got, tc.wantQA)
			}
			spec, _ := LoadAPISmokeSpecFromRig(dir, rig, tc.v)
			if APISmokeHasHTTPAPI(spec) != tc.wantAPI {
				t.Fatalf("APISmokeHasHTTPAPI=%v want %v (spec=%+v)", APISmokeHasHTTPAPI(spec), tc.wantAPI, spec)
			}
		})
	}
}

func TestBuildRuntimeSmokeShell_matrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		spec      APISmokeSpec
		mustHave  []string
		mustNot   []string
	}{
		{
			name: "api_and_static",
			spec: APISmokeSpec{
				ServerStart:       "go run ./cmd/server",
				Port:              8080,
				GETPaths:          []string{"/", "/api/links"},
				GETEmptyJSONArray: []string{"/api/links"},
				POSTProbes:        []POSTSmokeProbe{{Path: "/api/links", Body: `{"title":"t","url":"https://x"}`}},
				StaticAssets:      []string{"/static/app.js"},
			},
			mustHave:  []string{"/api/links", "POST", "/static/app.js"},
			mustNot:   []string{},
		},
		{
			name: "static_only",
			spec: APISmokeSpec{
				ServerStart:  "go run ./cmd/server",
				Port:         8080,
				GETPaths:     []string{"/"},
				StaticAssets: []string{"/static/app.js"},
			},
			mustHave: []string{"/static/app.js"},
			mustNot:  []string{"/api/", "-X POST"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := BuildRuntimeSmokeShell("/tmp/work", tc.spec)
			if script == "" {
				t.Fatal("empty script")
			}
			for _, s := range tc.mustHave {
				if !strings.Contains(script, s) {
					t.Fatalf("script missing %q:\n%s", s, script)
				}
			}
			for _, s := range tc.mustNot {
				if strings.Contains(script, s) {
					t.Fatalf("script must not contain %q:\n%s", s, script)
				}
			}
		})
	}
}

func TestRigFlowQARuntimeSmokeBlock_matrix(t *testing.T) {
	t.Parallel()
	const archAPI = `| GET | /api/links | list |
| POST | /api/links | create |
`
	const archStatic = `| GET | / | index |
`

	cases := []struct {
		name        string
		v           WorkflowValidation
		arch        string
		writeWeb    bool
		mustContain []string
		mustOmit    []string
	}{
		{
			name: "python_pytest",
			v: WorkflowValidation{
				LayoutRoot:      "backend",
				QAVerifyCommand: "python3 -m pytest -q",
				RequiredFiles:   []string{"backend/app.py"},
			},
			mustContain: []string{"Python", "pytest"},
			mustOmit:    []string{"go run ./cmd/server"},
		},
		{
			name: "go_library",
			v: WorkflowValidation{
				QAVerifyCommand: "cd pkg && go test ./...",
				RequiredFiles:   []string{"pkg/internal/x/x.go"},
			},
			mustContain: []string{"no web server", "skip"},
			mustOmit:    []string{"go run ./cmd/server"},
		},
		{
			name:     "go_web_static",
			v:        linkshelfLikeValidation(),
			arch:     archStatic,
			writeWeb: true,
			mustContain: []string{"static", "go run ./cmd/server"},
			mustOmit:    []string{"POST endpoints from SPEC", "POST endpoints"},
		},
		{
			name:     "go_web_api",
			v:        linkshelfLikeValidation(),
			arch:     archAPI,
			writeWeb: true,
			mustContain: []string{"go run ./cmd/server", "/api/links", "POST"},
			mustOmit:    []string{"Python rig"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			rig := "rig"
			rigDir := filepath.Join(dir, rig, "mayor", "rig")
			writeQARigDocs(t, rigDir, tc.arch, "")
			if tc.writeWeb {
				layout := strings.Trim(strings.TrimSpace(tc.v.LayoutRoot), "/")
				if layout == "" {
					layout = "linkshelf"
				}
				writeQARigWebIndex(t, rigDir, layout)
			}
			block := RigFlowQARuntimeSmokeBlock(dir, rig, tc.v)
			lower := strings.ToLower(block)
			for _, s := range tc.mustContain {
				if !strings.Contains(lower, strings.ToLower(s)) {
					t.Fatalf("block missing %q:\n%s", s, block)
				}
			}
			for _, s := range tc.mustOmit {
				if strings.Contains(lower, strings.ToLower(s)) {
					t.Fatalf("block must omit %q:\n%s", s, block)
				}
			}
		})
	}
}

func TestLoadAPISmokeSpecFromRig_matrix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	specBody := `# API
| GET | /api/items | JSON array |
| POST | /api/items | 201 |
`
	archBody := `# stale wrong path
| GET | /links | wrong |
`
	writeQARigDocs(t, rigDir, archBody, specBody)
	v := WorkflowValidation{LayoutRoot: "app"}
	spec, err := LoadAPISmokeSpecFromRig(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	// Merged parse: both docs contribute; SPEC has canonical /api/items
	if !APISmokeHasHTTPAPI(spec) {
		t.Fatalf("expected API paths: %+v", spec)
	}
	hasItems := false
	for _, p := range spec.GETPaths {
		if p == "/api/items" {
			hasItems = true
		}
	}
	if !hasItems {
		t.Fatalf("GET paths=%v", spec.GETPaths)
	}
}
