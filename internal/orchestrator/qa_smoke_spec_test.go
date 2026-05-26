package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
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

func TestStaticAssetsFromRig_staticPrefixFromArchitecture(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	webDir := filepath.Join(rigDir, "linkshelf", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := "| GET | /static/{file} | serves web/{file} |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	html := `<html><head><link href="style.css"><script src="app.js"></script></head></html>`
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/web/index.html"},
	}
	mapping := LoadWebStaticMappingFromRig(dir, rig, v)
	got := staticAssetsFromRig(rigDir, v, mapping)
	if !contains(got, "/static/app.js") || !contains(got, "/static/style.css") {
		t.Fatalf("assets=%v want /static/app.js and /static/style.css", got)
	}
	if contains(got, "/app.js") {
		t.Fatalf("must not probe bare /app.js when architecture uses /static/: %v", got)
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
	if !strings.HasPrefix(cmd, SmokeShellStrictPrefix) {
		t.Fatalf("want strict bash prefix, got %q", cmd)
	}
	if strings.Contains(cmd, `; test "$_gtok"`) || strings.Contains(cmd, `; curl -sf`) {
		t.Fatalf("probe steps must be &&-chained, not semicolon-separated: %q", cmd)
	}
}

func TestBuildRuntimeSmokeShell_failFastBeforeAPICheck(t *testing.T) {
	t.Parallel()
	base := "http://127.0.0.1:9"
	rootProbe := fmt.Sprintf(
		`_gtok=0; for _i in 1 2 3; do curl -sf --connect-timeout 1 --max-time 1 %s/ >/dev/null && _gtok=1 && break; done`,
		base,
	)
	strict := exec.Command("/bin/bash", "-c", SmokeShellStrictPrefix+rootProbe+` && test "$_gtok" = 1 && echo after-root`)
	if err := strict.Run(); err == nil {
		t.Fatal("strict probe should exit before later steps when / never returns 200")
	}
	loose := exec.Command("/bin/bash", "-c", rootProbe+`; test "$_gtok" = 1; echo after-root`)
	out, err := loose.CombinedOutput()
	if err != nil {
		t.Fatalf("loose probe should continue after failed root test: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "after-root") {
		t.Fatalf("loose probe should run steps after failed test, got %q", out)
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
