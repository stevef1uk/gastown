package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectSmokeResetPaths_explicitSection(t *testing.T) {
	t.Parallel()
	text := `
## Runtime smoke reset
- linkshelf.db
- data/cache/

## Other
`
	spec := APISmokeSpec{GETEmptyJSONArray: []string{"/api/links"}}
	got := collectSmokeResetPaths(text, spec)
	if !contains(got, "linkshelf.db") || !contains(got, "data/cache/") {
		t.Fatalf("got %v", got)
	}
}

func TestCollectSmokeResetPaths_inferredFromArchitecture(t *testing.T) {
	t.Parallel()
	text := `
Opens ` + "`linkshelf.db`" + ` in the working directory.
1. ` + "`db, err := sql.Open(\"sqlite3\", \"./linkshelf.db\")`" + `
`
	spec := APISmokeSpec{GETEmptyJSONArray: []string{"/api/links"}}
	got := collectSmokeResetPaths(text, spec)
	if !contains(got, "linkshelf.db") {
		t.Fatalf("got %v", got)
	}
}

func TestCollectSmokeResetPaths_inferredBareBacktickFilename(t *testing.T) {
	t.Parallel()
	text := "Open SQLite file `linkshelf.db` in the current working directory."
	spec := APISmokeSpec{GETEmptyJSONArray: []string{"/api/links"}}
	got := collectSmokeResetPaths(text, spec)
	if !contains(got, "linkshelf.db") {
		t.Fatalf("want linkshelf.db inferred from bare backtick filename, got %v", got)
	}
}

func TestCollectSmokeResetPaths_skipsInferenceWithoutEmptyArrayContract(t *testing.T) {
	t.Parallel()
	text := "Opens `linkshelf.db`"
	spec := APISmokeSpec{}
	got := collectSmokeResetPaths(text, spec)
	if len(got) != 0 {
		t.Fatalf("want no inferred paths without GETEmptyJSONArray, got %v", got)
	}
}

func TestCollectSmokeResetPaths_rejectsUnsafePaths(t *testing.T) {
	t.Parallel()
	text := `
## Smoke reset
- ../escape.db
- :memory:
- /abs.db
`
	spec := APISmokeSpec{GETEmptyJSONArray: []string{"/api/x"}}
	got := collectSmokeResetPaths(text, spec)
	for _, p := range got {
		if strings.Contains(p, "..") || strings.Contains(p, ":") || strings.HasPrefix(p, "/") {
			t.Fatalf("unsafe path %q in %v", p, got)
		}
	}
}

func TestBuildRuntimeSmokeShell_includesDocReset(t *testing.T) {
	t.Parallel()
	spec := APISmokeSpec{
		Port:              8080,
		GETPaths:          []string{"/", "/api/links"},
		GETEmptyJSONArray: []string{"/api/links"},
		ResetPaths:        []string{"linkshelf.db"},
		ServerStart:       "go run ./cmd/server",
	}
	cmd := BuildRuntimeSmokeShell("/tmp/work", spec)
	if !strings.Contains(cmd, "rm -f -- 'linkshelf.db'") {
		t.Fatalf("want reset rm, got %q", cmd)
	}
	if !strings.Contains(cmd, "GT_SMOKE:reset:linkshelf.db") {
		t.Fatalf("want reset marker, got %q", cmd)
	}
}

func TestDeriveRuntimeSmokeServerStart_fromQACommand(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		QAVerifyCommand: "cd linkshelf && go run ./cmd/server/main.go & curl -s http://127.0.0.1:8080/",
	}
	got := deriveRuntimeSmokeServerStart(v, "")
	if got != "go run ./cmd/server/main.go" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadAPISmokeSpecFromRig_enrichesResetPaths(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := `
| GET | /api/items | 200, JSON array when empty |
Opens ` + "`app.db`" + ` via sql.Open("sqlite3", "./app.db").
`
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "app"}
	spec, err := LoadAPISmokeSpecFromRig(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(spec.GETEmptyJSONArray, "/api/items") {
		t.Fatalf("empty array paths=%v", spec.GETEmptyJSONArray)
	}
	if !contains(spec.ResetPaths, "app.db") {
		t.Fatalf("reset paths=%v", spec.ResetPaths)
	}
}
