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
| GET | /api/stream/prices | SSE stream of live price updates |
| POST | /api/links | 201 |
`
	spec := parseAPISmokeSpecText(text, WorkflowValidation{})
	if len(spec.Probes) < 4 {
		t.Fatalf("probes=%v", spec.Probes)
	}
	var hasEmptyAPI, hasStreamAPI bool
	for _, p := range spec.Probes {
		if p.Path == "/api/links" && p.Method == "GET" && p.Expect == SmokeExpectEmptyJSONArray {
			hasEmptyAPI = true
		}
		if p.Path == "/api/stream/prices" && p.Method == "GET" && p.Expect == SmokeExpectStream {
			hasStreamAPI = true
		}
	}
	if !hasEmptyAPI {
		t.Fatalf("want empty-array GET probe, got %v", spec.Probes)
	}
	if !hasStreamAPI {
		t.Fatalf("want stream GET probe, got %v", spec.Probes)
	}
}

func TestBuildRuntimeSmokeShell_streamingProbeHandlesExit28(t *testing.T) {
	t.Parallel()
	spec := APISmokeSpec{
		Port: 8080,
		Probes: []HTTPEndpointProbe{
			{Method: "GET", Path: "/api/stream/prices", Expect: SmokeExpectStream, Source: "api"},
		},
		ServerStart: "uvicorn main:app --port 8080",
	}
	cmd := BuildRuntimeSmokeShell("/tmp/work", spec)
	if !strings.Contains(cmd, "test $_rc -eq 28") {
		t.Fatalf("streaming probe must tolerate exit 28 on infinite streams: %q", cmd)
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
