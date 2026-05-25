package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// TestRequiresQARuntimeSmoke_matrix mirrors orchestrator.WorkflowNeedsQARuntimeSmoke via gt-agent wrapper.
func TestRequiresQARuntimeSmoke_matrix(t *testing.T) {
	t.Parallel()
	const archAPI = `| GET | /api/links | JSON array |
| POST | /api/links | create |
`
	const archStatic = `| GET | / | index |
`

	cases := []struct {
		name   string
		v      orchestrator.WorkflowValidation
		arch   string
		web    bool
		want   bool
	}{
		{
			name: "go_lib",
			v: orchestrator.WorkflowValidation{
				QAVerifyCommand: "cd pkg && go test ./...",
				RequiredFiles:   []string{"pkg/foo.go"},
			},
			want: false,
		},
		{
			name: "go_web_api",
			v:    linkshelfWebProfile(),
			arch: archAPI,
			web:  true,
			want: true,
		},
		{
			name: "go_web_static",
			v:    linkshelfWebProfile(),
			arch: archStatic,
			web:  true,
			want: true,
		},
		{
			name: "go_server_only",
			v: orchestrator.WorkflowValidation{
				LayoutRoot:      "linkshelf",
				QAVerifyCommand: "cd linkshelf && go test ./...",
				RequiredFiles:   []string{"linkshelf/cmd/server/main.go"},
			},
			arch: archAPI,
			want: false,
		},
		{
			name: "python_pytest",
			v: orchestrator.WorkflowValidation{
				QAVerifyCommand: "python3 -m pytest -q",
				RequiredFiles:   []string{"backend/main.py"},
			},
			want: false,
		},
		{
			name: "python_flask_api",
			v: orchestrator.WorkflowValidation{
				QAVerifyCommand: "python3 -m pytest -q",
				RequiredFiles:   []string{"backend/app.py"},
			},
			arch: archAPI,
			want: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			rig := "mockrig"
			rigDir := filepath.Join(dir, rig, "mayor", "rig")
			if tc.arch != "" {
				if err := os.MkdirAll(rigDir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(tc.arch), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.web {
				writeLinkshelfWebIndexForMatrix(t, rigDir)
			}
			if got := requiresQARuntimeSmoke(dir, rig, tc.v); got != tc.want {
				t.Fatalf("requiresQARuntimeSmoke=%v want %v", got, tc.want)
			}
		})
	}
}

func writeLinkshelfWebIndexForMatrix(t *testing.T, rigDir string) {
	t.Helper()
	webDir := filepath.Join(rigDir, "linkshelf", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"index.html": `<!DOCTYPE html><html><script src="/static/app.js"></script></html>`,
		"app.js":     "ok();\n",
	} {
		if err := os.WriteFile(filepath.Join(webDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestIsQARuntimeSmokeCommandOK_matrix checks gt-agent accepts/rejects smoke CMDs per doc shape.
func TestIsQARuntimeSmokeCommandOK_matrix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeLinkshelfArchitecture(t, rigDir, false)
	vAPI := linkshelfWebProfile()

	dirStatic := t.TempDir()
	rigStatic := "rig2"
	rigDirStatic := filepath.Join(dirStatic, rigStatic, "mayor", "rig")
	if err := os.MkdirAll(rigDirStatic, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDirStatic, "architecture.md"), []byte("| GET | / | index |\n"), 0644); err != nil {
		t.Fatal(err)
	}
	vStatic := linkshelfWebProfile()

	fullAPI := `cd mockrig/mayor/rig/linkshelf && go run ./cmd/server & curl -sf http://127.0.0.1:8080/ && curl -s http://127.0.0.1:8080/api/bookmarks | grep -q '[]' && curl -sf -X POST -H 'Content-Type: application/json' -d '{"title":"x","url":"https://a"}' http://127.0.0.1:8080/api/bookmarks`
	staticOnly := `cd rig2/mayor/rig/linkshelf && go run ./cmd/server & curl -sf http://127.0.0.1:8080/ && curl -sf http://127.0.0.1:8080/static/app.js`

	cases := []struct {
		name    string
		town    string
		rigName string
		v       orchestrator.WorkflowValidation
		cmd     string
		want    bool
	}{
		{name: "api_full", town: dir, rigName: rig, v: vAPI, cmd: fullAPI, want: true},
		{name: "api_missing_post", town: dir, rigName: rig, v: vAPI, cmd: `go run ./cmd/server & curl -sf http://127.0.0.1:8080/api/bookmarks`, want: false},
		{name: "api_go_run_only", town: dir, rigName: rig, v: vAPI, cmd: "go run ./cmd/server", want: false},
		{name: "static_root_curl", town: dirStatic, rigName: rigStatic, v: vStatic, cmd: staticOnly, want: true},
		{name: "static_fake_api_curl", town: dirStatic, rigName: rigStatic, v: vStatic, cmd: `go run ./cmd/server & curl -sf http://127.0.0.1:8080/api/links`, want: false},
		{name: "lib_profile", town: dir, rigName: rig, v: orchestrator.WorkflowValidation{QAVerifyCommand: "cd pkg && go test ./...", RequiredFiles: []string{"pkg/x.go"}}, cmd: fullAPI, want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isQARuntimeSmokeCommandOK(tc.cmd, tc.town, tc.rigName, tc.v); got != tc.want {
				t.Fatalf("isQARuntimeSmokeCommandOK=%v want %v for %q", got, tc.want, tc.cmd)
			}
		})
	}
}
