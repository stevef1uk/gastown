package orchestrator

import (
	"strings"
	"testing"
)

func TestDefaultScaffoldDetectsApplicationStack(t *testing.T) {
	for _, tt := range []struct {
		name, stack, template string
		files                 []string
	}{
		{"python with browser tests", "python", "Dockerfile.python", []string{"app/backend/app/main.py", "app/test/package.json", "app/test/playwright.config.ts", "app/test/e2e.spec.ts"}},
		{"go with browser tests", "go", "Dockerfile", []string{"app/go.mod", "app/cmd/server/main.go", "app/test/package.json", "app/test/e2e.spec.ts"}},
		{"node", "node", "Dockerfile.node", []string{"app/package.json", "app/src/server.ts", "app/test/e2e.spec.ts"}},
		{"python and node frontend", "hybrid", "Dockerfile.hybrid", []string{"app/backend/pyproject.toml", "app/frontend/package.json", "app/test/e2e.spec.ts"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := append(append([]string{}, tt.files...), "app/Dockerfile", "app/test/docker-compose.test.yml")
			v := WorkflowValidation{LayoutRoot: "app", RequiredFiles: files}
			plan := defaultScaffoldPlan(&v)
			if plan.Stack != tt.stack {
				t.Fatalf("stack = %q, want %q", plan.Stack, tt.stack)
			}
			templates, _ := selectDefaultTemplates(&v)
			if templates[tt.template] != "Dockerfile" {
				t.Fatalf("wrong application Dockerfile selection: %v", templates)
			}
			values := buildTemplateValues(nil, 8000, "single-container", &v)
			data, err := townAssets.ReadFile(rigInitTemplateDir + "/" + tt.template)
			if err != nil {
				t.Fatal(err)
			}
			if rendered := renderTemplate(string(data), values); strings.Contains(rendered, "{{") {
				t.Fatalf("unresolved Dockerfile template variables: %s", rendered)
			}
		})
	}
}

func TestBrowserVerificationFollowsDockerPrerequisites(t *testing.T) {
	files := []string{"app/test/e2e.spec.ts", "app/test/playwright.config.ts", "app/Dockerfile", "app/docker-compose.yml", "app/test/docker-compose.test.yml", "app/backend/app/main.py"}
	ordered := OrderRequiredFilesForImplementation(files)
	lastPrerequisite := -1
	firstBrowser := len(ordered)
	for i, path := range ordered {
		if IsE2ETestPath(path) {
			if i < firstBrowser {
				firstBrowser = i
			}
		} else {
			lastPrerequisite = i
		}
	}
	if firstBrowser <= lastPrerequisite {
		t.Fatalf("browser Verify would run before its build prerequisites: %v", ordered)
	}
}

func TestDefaultScaffoldUsesRunnerWhenApplicationPathsAreAbsent(t *testing.T) {
	for runner, want := range map[string]string{"pytest": "python", "python": "python", "node": "node", "jest": "node", "go": "go"} {
		v := WorkflowValidation{TestRunner: runner, RequiredFiles: []string{"test/playwright.config.ts", "test/package.json"}}
		if got := defaultScaffoldPlan(&v).Stack; got != want {
			t.Errorf("runner %s: stack=%s, want %s", runner, got, want)
		}
	}
}
