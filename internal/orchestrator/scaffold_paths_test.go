package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestScaffoldNestedComposeBuildsFromLayoutRoot(t *testing.T) {
	for _, layout := range []string{"workstation", "."} {
		for _, harness := range []string{"test", "checks/browser"} {
			t.Run(layout+"/"+harness, func(t *testing.T) {
				town := t.TempDir()
				required := func(p string) string { return filepath.ToSlash(filepath.Join(layout, p)) }
				composePath := required(harness + "/docker-compose.test.yml")
				v := WorkflowValidation{
					LayoutRoot: layout, DevServerPort: 8123,
					RequiredFiles:  []string{required("Dockerfile"), composePath, required(harness + "/playwright.config.ts")},
					DeliveryPhases: []DeliveryPhase{{ID: "release", RequiredFiles: []string{composePath, required(harness + "/playwright.config.ts")}}},
				}
				rigDir := filepath.Join(town, "demo", "mayor", "rig")
				meta := filepath.Join(rigDir, ".gastown")
				if err := os.MkdirAll(meta, 0755); err != nil {
					t.Fatal(err)
				}
				raw, err := marshalRigProfileJSON(rigProfileEnvelope{Validation: v})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(meta, "workflow-profile.json"), raw, 0644); err != nil {
					t.Fatal(err)
				}
				if _, err := ScaffoldRigIntegrationTemplates(town, "demo", nil); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(rigDir, composePath))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "${APP_PORT}") || strings.Contains(string(data), "{{") {
					t.Fatalf("unresolved scaffold variables:\n%s", data)
				}
				var doc struct {
					Services map[string]struct {
						Build                struct{ Context, Dockerfile, Target string }
						Volumes, Environment []string
						Command              string
						Healthcheck          struct{ Test []string }
					}
				}
				if err := yaml.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				composeDir := filepath.Dir(filepath.Join(rigDir, composePath))
				root := filepath.Join(rigDir, layout)
				for name, svc := range doc.Services {
					context := filepath.Clean(filepath.Join(composeDir, svc.Build.Context))
					if context != root {
						t.Errorf("%s builds in %s, want %s", name, context, root)
					}
					df := svc.Build.Dockerfile
					if df == "" {
						df = "Dockerfile"
					}
					content, err := os.ReadFile(filepath.Join(context, df))
					if err != nil {
						t.Fatal(err)
					}
					if svc.Build.Target != "" && !strings.Contains(string(content), "AS "+svc.Build.Target) {
						t.Errorf("%s target %s absent from %s", name, svc.Build.Target, df)
					}
				}
				app := doc.Services["app"]
				if !containsString(app.Environment, "PORT=8123") || !containsString(app.Healthcheck.Test, "http://localhost:8123/") {
					t.Fatalf("wrong port configuration: %+v", app)
				}
				runner := doc.Services["playwright"]
				mount := strings.SplitN(runner.Volumes[0], ":", 2)[0]
				if filepath.Clean(filepath.Join(composeDir, mount)) != root {
					t.Errorf("runner mount is not the layout root: %s", mount)
				}
				if !strings.Contains(runner.Command, "--config="+harness+"/playwright.config.ts") {
					t.Errorf("runner does not use required config: %s", runner.Command)
				}
			})
		}
	}
}
