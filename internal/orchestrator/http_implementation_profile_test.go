package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHTTPImplementationProfile_embeddedDefault(t *testing.T) {
	t.Parallel()
	InvalidateHTTPProfileCacheForTest()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	p := LoadHTTPImplementationProfile("", "", v)
	if p.ID != "go-stdlib-servemux" || !p.Enabled {
		t.Fatalf("got %+v", p)
	}
	if len(p.WriteGuards) < 2 {
		t.Fatalf("expected write guards, got %d", len(p.WriteGuards))
	}
}

func TestLoadHTTPImplementationProfile_townOverride(t *testing.T) {
	t.Parallel()
	InvalidateHTTPProfileCacheForTest()
	town := t.TempDir()
	dir := filepath.Join(town, "orchestrator", httpProfilesTownSubdir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	custom := `{"id":"go-stdlib-servemux","stack":"go-stdlib-servemux","enabled":true,"web_disk_dir":"assets","write_guards":[]}`
	if err := os.WriteFile(filepath.Join(dir, "go-stdlib-servemux.json"), []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	p := LoadHTTPImplementationProfile(town, "rig1", WorkflowValidation{LayoutRoot: "app"})
	if p.WebDiskDir != "assets" {
		t.Fatalf("town override web_disk_dir: got %q", p.WebDiskDir)
	}
}

func TestLoadHTTPImplementationProfile_rigConfigSelectsGeneric(t *testing.T) {
	t.Parallel()
	InvalidateHTTPProfileCacheForTest()
	town := t.TempDir()
	rig := "myrig"
	cfgDir := filepath.Join(town, rig, "mayor", "rig", rigProfileDir)
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"profile":"generic"}`
	if err := os.WriteFile(filepath.Join(cfgDir, httpProfileRigFile), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	p := LoadHTTPImplementationProfile(town, rig, WorkflowValidation{})
	if p.Enabled {
		t.Fatalf("generic profile should disable stack guards, got %+v", p)
	}
}

func TestHandlerWriteGuardIssues_fromJSON(t *testing.T) {
	t.Parallel()
	InvalidateHTTPProfileCacheForTest()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	p := LoadHTTPImplementationProfile("", "", v)
	p.StaticURLPrefix = "/static"
	body := `path := filepath.Join("..", "..", "web", name)`
	issues := p.HandlerWriteGuardIssues(body)
	if len(issues) == 0 {
		t.Fatal("expected bad web join issue")
	}
}

func TestFormatTraversalHint_usesTemplateVars(t *testing.T) {
	t.Parallel()
	InvalidateHTTPProfileCacheForTest()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	h := LoadHTTPImplementationProfile("", "", v).FormatTraversalRedirectHint("linkshelf/internal/api/handlers.go", v)
	if !strings.Contains(h, "/static/") || !strings.Contains(h, "RequestURI") {
		t.Fatalf("missing expanded hint:\n%s", h)
	}
}
