package specprofile

import (
	"context"
	"encoding/json"
	"fmt"
)

// ScaffoldPlan represents the scaffolding plan for a rig
type ScaffoldPlan struct {
	Kind        string              `json:"kind"`         // "host-run", "single-container", "multi-service"
	Stack       string              `json:"stack"`        // "python", "node", "go", "hybrid", "generic"
	LayoutRoot  string              `json:"layout_root"`
	Port        int                 `json:"port"`
	HarnessDir  string              `json:"harness_dir"`  // e.g. "test", "" for layout root
	BaseURL     string              `json:"base_url"`     // e.g. "http://app:8000" or "http://host.docker.internal:8080"
	Services    []ScaffoldService   `json:"services"`
}

// ScaffoldService represents a service in the scaffold plan
type ScaffoldService struct {
	Name       string            `json:"name"`
	BuildDir   string            `json:"build_dir"`   // relative to layout root, "." for root
	Image      string            `json:"image"`       // if no build
	Port       int               `json:"port"`
	Stack      string            `json:"stack"`       // "python", "node", "go", "generic"
	Public     bool              `json:"public"`      // exposed to browser
	Health     string            `json:"health"`      // healthcheck command
	Env        map[string]string `json:"env"`         // environment variables
}

// ExtractScaffoldPlan uses LLM to analyze SPEC and produce scaffold plan
func ExtractScaffoldPlan(ctx context.Context, townRoot, rig, specText string) (*ScaffoldPlan, error) {
	endpoint, model := ResolveLLMForSpecIndex(townRoot)

	system := scaffoldPlanSystemPrompt()

	user := fmt.Sprintf(`SPECIFICATION:
%s

Extract the scaffold plan as JSON matching the ScaffoldPlan schema.`, specText)

	content, err := chatCompletionJSON(ctx, endpoint, model, system, user)
	if err != nil {
		return nil, err
	}

	var plan ScaffoldPlan
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return nil, fmt.Errorf("parse scaffold plan JSON: %w\nContent: %.200s", err, content)
	}

	// Validate and fill defaults
	if plan.Kind == "" {
		plan.Kind = "host-run"
	}
	if plan.Stack == "" {
		plan.Stack = "generic"
	}
	if plan.Port == 0 {
		plan.Port = 8080
	}
	if plan.BaseURL == "" {
		if plan.Kind == "host-run" {
			plan.BaseURL = fmt.Sprintf("http://host.docker.internal:%d", plan.Port)
		} else if plan.Kind == "single-container" {
			plan.BaseURL = fmt.Sprintf("http://app:%d", plan.Port)
		} else if len(plan.Services) > 0 {
			for _, svc := range plan.Services {
				if svc.Public {
					plan.BaseURL = fmt.Sprintf("http://%s:%d", svc.Name, svc.Port)
					break
				}
			}
		}
		if plan.BaseURL == "" {
			plan.BaseURL = fmt.Sprintf("http://localhost:%d", plan.Port)
		}
	}
	if plan.HarnessDir == "" {
		plan.HarnessDir = "test"
	}

	return &plan, nil
}

func scaffoldPlanSystemPrompt() string {
	return `You are a build-system assistant. Given a project SPECIFICATION (markdown), emit a single JSON object only—no prose, no markdown fences.

The JSON must match this shape:
{
  "kind": "host-run" | "single-container" | "multi-service",
  "stack": "python" | "node" | "go" | "hybrid" | "generic",
  "layout_root": "string",
  "port": number,
  "harness_dir": "test" | "",
  "base_url": "string",
  "services": [
    {
      "name": "string",
      "build_dir": "string",
      "image": "string",
      "port": number,
      "stack": "python" | "node" | "go" | "generic",
      "public": boolean,
      "health": "string",
      "env": { "KEY": "value" }
    }
  ]
}

Rules for classification:
- kind "host-run": The web app runs on the HOST machine (not in Docker). Playwright container reaches it via host.docker.internal. Typical for simple Go quickstart projects where the SPEC doesn't mention Docker/containers. The app is built and run locally (go run, python main.py, npm start) and Playwright container uses extra_hosts to reach host.docker.internal.

- kind "single-container": The entire application is packaged into ONE Docker image (possibly multi-stage build). The SPEC describes a single container deployment (e.g. "single Docker container", "multi-stage build", "one command to run"). The production Dockerfile is at layout root. Test harness docker-compose.test.yml brings up the app container + playwright over internal Docker network.

- kind "multi-service": Multiple independent services, each with its own Dockerfile/Docker image. The SPEC describes multiple services (e.g., "web", "gateway", "ledger", "assistant", "db") each in their own container. Look for keywords: "each in its own Docker container", "microservices", "multi-service", "multiple services", "separate containers", or a docker-compose.yml with multiple build: services. The SPEC's docker-compose.yml will have multiple build: entries.

Stack detection:
- "python": FastAPI, uvicorn, pyproject.toml, uv project, requirements.txt, Django, Flask
- "node": Next.js, React, TypeScript, npm, package.json, vite, tsx
- "go": go.mod, golang, cmd/server
- "hybrid": BOTH python backend AND node frontend (e.g. FastAPI + Next.js static export)
- "generic": fallback

Services extraction:
- For multi-service: parse the SPEC's docker-compose.yml for services with build: or image:
- For single-container: create one service "app" with build_dir "." at layout root
- For host-run: no services needed (app runs on host)

Stack per service:
- python: FastAPI, uvicorn, pyproject.toml, uv
- node: Next.js, React, TypeScript, npm
- go: go.mod, golang

Output JSON only.`
}