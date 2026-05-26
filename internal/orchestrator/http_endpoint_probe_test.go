package orchestrator

import (
	"strings"
	"testing"
)

func TestParseAPISmokeSpecText_buildsProbes(t *testing.T) {
	t.Parallel()
	text := `
| GET | / | index |
| GET | /api/links | JSON array when empty |
| POST | /api/links | 201 |
`
	spec := parseAPISmokeSpecText(text, WorkflowValidation{})
	if len(spec.Probes) < 3 {
		t.Fatalf("probes=%v", spec.Probes)
	}
	var hasEmptyAPI bool
	for _, p := range spec.Probes {
		if p.Path == "/api/links" && p.Method == "GET" && p.Expect == SmokeExpectEmptyJSONArray {
			hasEmptyAPI = true
		}
	}
	if !hasEmptyAPI {
		t.Fatalf("want empty-array GET probe, got %v", spec.Probes)
	}
}

func TestBuildRuntimeSmokeShell_usesProbeCurls(t *testing.T) {
	t.Parallel()
	spec := APISmokeSpec{
		Port: 8080,
		Probes: []HTTPEndpointProbe{
			{Method: "GET", Path: "/", Source: "api"},
			{Method: "GET", Path: "/api/items", Expect: SmokeExpectEmptyJSONArray, Source: "api"},
			{Method: "POST", Path: "/api/items", Body: `{"x":1}`, Source: "api"},
		},
		ServerStart: "python3 -m http.server 8080",
	}
	cmd := BuildRuntimeSmokeShell("/tmp/work", spec)
	for _, want := range []string{
		"GT_SMOKE:GET:/api/items",
		`= "[]"`,
		"GT_SMOKE:POST:/api/items",
		"-X POST",
		"python3 -m http.server 8080",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %q in %q", want, cmd)
		}
	}
}

func TestAPISmokeHasHTTPAPI_ignoresStaticProbes(t *testing.T) {
	t.Parallel()
	spec := APISmokeSpec{
		Probes: []HTTPEndpointProbe{
			{Method: "GET", Path: "/static/app.js", Source: "static"},
		},
	}
	syncAPISmokeSpecDerivedFields(&spec)
	if APISmokeHasHTTPAPI(spec) {
		t.Fatal("static-only must not count as API smoke")
	}
}
