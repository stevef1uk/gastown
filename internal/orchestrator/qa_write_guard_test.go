package orchestrator

import "testing"

func linkshelfProfile() WorkflowValidation {
	return WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/web/app.js",
			"linkshelf/internal/api/handlers.go",
		},
	}
}

func TestQACommandMutatesLayoutSource_sed(t *testing.T) {
	t.Parallel()
	v := linkshelfProfile()
	cmd := `cd testrig/mayor/rig && sed -i 's|/static/app.js|/app.js|' linkshelf/web/index.html`
	if path, ok := QACommandMutatesLayoutSource(cmd, v); !ok || path == "" {
		t.Fatalf("expected block, got path=%q ok=%v", path, ok)
	}
}

func TestQACommandMutatesLayoutSource_allowsRead(t *testing.T) {
	t.Parallel()
	v := linkshelfProfile()
	cmd := `cd testrig/mayor/rig && head -n 20 linkshelf/web/index.html`
	if _, ok := QACommandMutatesLayoutSource(cmd, v); ok {
		t.Fatal("expected read-only head to be allowed")
	}
}

func TestQACommandMutatesLayoutSource_allowsReadOnlySed(t *testing.T) {
	t.Parallel()
	v := linkshelfProfile()
	for _, cmd := range []string{
		`cd testrig/mayor/rig && sed -n '1,140p' linkshelf/web/index.html`,
		`cd testrig/mayor/rig && sed 's|old|new|' linkshelf/web/index.html`,
	} {
		if path, ok := QACommandMutatesLayoutSource(cmd, v); ok {
			t.Errorf("read-only command blocked as a write to %q: %s", path, cmd)
		}
	}
}

func TestQACommandMutatesLayoutSource_blocksInPlaceSed(t *testing.T) {
	t.Parallel()
	v := linkshelfProfile()
	for _, cmd := range []string{
		`cd testrig/mayor/rig && sed -i 's|old|new|' linkshelf/web/index.html`,
		`cd testrig/mayor/rig && sed -i '' 's|old|new|' linkshelf/web/index.html`,
	} {
		if path, ok := QACommandMutatesLayoutSource(cmd, v); !ok || path == "" {
			t.Errorf("in-place edit was not blocked: %s", cmd)
		}
	}
}

func TestQACommandMutatesLayoutSource_allowsChainedVerifyWithNpmInstall(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "finally",
		RequiredFiles: []string{
			"finally/frontend/components/PortfolioSummary.tsx",
			"finally/test/portfolio-visualisation-trading.spec.ts",
		},
	}
	cmd := `cd testgt4/mayor/rig && (cd finally/frontend && npm install --ignore-scripts) && (cd finally/frontend && npm run build) && test -f finally/frontend/components/PortfolioSummary.tsx && test -f finally/test/portfolio-visualisation-trading.spec.ts`
	if path, ok := QACommandMutatesLayoutSource(cmd, v); ok {
		t.Fatalf("expected verify command with npm install not to be blocked as a write, but blocked on %q", path)
	}
}
