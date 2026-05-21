package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAPISmokeSpecText_linkshelfTable(t *testing.T) {
	text := `
| GET | / | 200, serve web/index.html | — |
| GET | /api/links | 200, JSON array | — |
| POST | /api/links | 201, JSON of created link | 400 |
visit http://localhost:8080
`
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	spec := parseAPISmokeSpecText(text, v)
	if spec.Port != 8080 {
		t.Fatalf("port=%d", spec.Port)
	}
	if !contains(spec.GETPaths, "/api/links") {
		t.Fatalf("GET paths=%v", spec.GETPaths)
	}
	if !contains(spec.GETEmptyJSONArray, "/api/links") {
		t.Fatalf("empty array paths=%v", spec.GETEmptyJSONArray)
	}
	if len(spec.POSTProbes) != 1 || spec.POSTProbes[0].Path != "/api/links" {
		t.Fatalf("POST=%v", spec.POSTProbes)
	}
}

func TestLoadAPISmokeSpecFromRig_readsSPEC(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	specBody := "# Spec\n| GET | /api/bookmarks | 200, JSON array | — |\n| POST | /api/bookmarks | 201 | — |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(specBody), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", RequiredFiles: []string{"linkshelf/web/index.html"}}
	got, err := LoadAPISmokeSpecFromRig(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got.GETPaths, "/api/bookmarks") || len(got.POSTProbes) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildRuntimeSmokeShell_includesPOSTAndEmptyArray(t *testing.T) {
	spec := APISmokeSpec{
		Port:              8080,
		GETPaths:          []string{"/", "/api/links"},
		GETEmptyJSONArray: []string{"/api/links"},
		POSTProbes:        []POSTSmokeProbe{{Path: "/api/links", Body: `{"title":"qa","url":"https://example.com"}`}},
		StaticAssets:      []string{"/app.js"},
	}
	cmd := BuildRuntimeSmokeShell("testgt3/mayor/rig/linkshelf", spec)
	if !strings.Contains(cmd, `/api/links`) || !strings.Contains(cmd, "POST") {
		t.Fatalf("got %q", cmd)
	}
	if !strings.Contains(cmd, `test "$(curl -s`) || !strings.Contains(cmd, `= "[]"`) {
		t.Fatalf("want empty-array check: %q", cmd)
	}
	if !strings.Contains(cmd, "/app.js") {
		t.Fatalf("want static asset curl: %q", cmd)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
