package orchestrator

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseSmokeFailureFromOutput_lastMarker(t *testing.T) {
	t.Parallel()
	out := "starting\nGT_SMOKE:wait_root\nGT_SMOKE:GET:/static/app.js\ncurl: (22) The requested URL returned error: 404"
	d := ParseSmokeFailureFromOutput(out)
	if d.FailedStep != "GET:/static/app.js" {
		t.Fatalf("FailedStep=%q", d.FailedStep)
	}
	if !strings.Contains(d.RawTail, "404") {
		t.Fatalf("RawTail=%q", d.RawTail)
	}
}

func TestBuildRuntimeSmokeShell_hasGTSmokeMarkers(t *testing.T) {
	t.Parallel()
	script := BuildRuntimeSmokeShell("/tmp/work", APISmokeSpec{
		ServerStart:  "go run ./cmd/server",
		Port:         8080,
		GETPaths:     []string{"/", "/api/links"},
		StaticAssets: []string{"/static/app.js"},
		POSTProbes:   []POSTSmokeProbe{{Path: "/api/links", Body: `{}`}},
	})
	for _, want := range []string{"GT_SMOKE:wait_root", "GT_SMOKE:GET:/static/app.js", "GT_SMOKE:GET:/api/links", "GT_SMOKE:POST:/api/links"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestImplementationReworkPathsForSmoke_includesServerMain(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
			"linkshelf/web/index.html",
			"linkshelf/cmd/server/main.go",
		},
	}
	paths := implementationReworkPathsForSmoke(v)
	joined := strings.Join(paths, " ")
	// Frontend files (web/*.html) are intentionally excluded — smoke failures
	// are caused by the server not serving them, not by the files themselves.
	for _, want := range []string{"handlers.go", "cmd/server/main.go"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("paths=%v missing %q", paths, want)
		}
	}
	if strings.Contains(joined, "web/index.html") {
		t.Fatalf("paths=%v should not contain frontend files", paths)
	}
}

func TestImplementationVerifyNeedsRuntimeRework(t *testing.T) {
	t.Parallel()
	if !ImplementationVerifyNeedsRuntimeRework(fmt.Errorf("implementation runtime smoke failed: exit 22")) {
		t.Fatal("expected runtime smoke")
	}
	if ImplementationVerifyNeedsRuntimeRework(fmt.Errorf("go test failed")) {
		t.Fatal("compile-only should not match")
	}
}

func TestFormatImplementationSmokeFailureBlock_smokeStep(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("implementation runtime smoke failed: exit 22\nGT_SMOKE:GET:/static/app.js\ncurl: (22) 404")
	block := FormatImplementationSmokeFailureBlock(t.TempDir(), "rig", WorkflowValidation{LayoutRoot: "linkshelf"}, err, []string{"bd-1"})
	if !strings.Contains(block, "/static/app.js") || !strings.Contains(block, "bd-1") {
		t.Fatalf("block=%q", block)
	}
}
