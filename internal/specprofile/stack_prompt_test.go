package specprofile

import (
	"strings"
	"testing"
)

func TestDetectStackFromSpec_Go(t *testing.T) {
	specs := []string{
		"This is a Go project with go.mod and main.go",
		"Requires go test ./... and go build",
		"Uses gin framework for HTTP",
		"Go module with _test.go files",
	}
	for _, s := range specs {
		got := detectStackFromSpec(s)
		if got != StackGo {
			t.Fatalf("spec %q: got %q want %q", s, got, StackGo)
		}
	}
}

func TestDetectStackFromSpec_Python(t *testing.T) {
	specs := []string{
		"Python FastAPI project with requirements.txt and uv.lock",
		"pytest tests/ and uvicorn main:app",
		"main.py with pyproject.toml",
		"test_*.py files for pytest",
	}
	for _, s := range specs {
		got := detectStackFromSpec(s)
		if got != StackPython {
			t.Fatalf("spec %q: got %q want %q", s, got, StackPython)
		}
	}
}

func TestDetectStackFromSpec_NodeJS(t *testing.T) {
	specs := []string{
		"Next.js project with package.json and npm install",
		"TypeScript React with pnpm-lock.yaml",
		"npm ci && npm run build with vite",
		"package.json with @types/jest",
	}
	for _, s := range specs {
		got := detectStackFromSpec(s)
		if got != StackNodeJS {
			t.Fatalf("spec %q: got %q want %q", s, got, StackNodeJS)
		}
	}
}

func TestDetectStackFromSpec_Docker(t *testing.T) {
	specs := []string{
		"Dockerfile for containerized deployment",
		"docker-compose.yml with multiple services",
		"Container orchestration with docker compose",
	}
	for _, s := range specs {
		got := detectStackFromSpec(s)
		if got != StackDocker {
			t.Fatalf("spec %q: got %q want %q", s, got, StackDocker)
		}
	}
}

func TestDetectStackFromSpec_Mixed_GoWins(t *testing.T) {
	// Go should win when multiple stacks present (Go prioritized)
	spec := "Go project with go.mod but also has package.json for frontend"
	got := detectStackFromSpec(spec)
	if got != StackGo {
		t.Fatalf("mixed Go+Node: got %q want %q", got, StackGo)
	}
}

func TestStackDeliveryPhaseGuidance_Go(t *testing.T) {
	guidance := stackDeliveryPhaseGuidance(StackGo)
	if !strings.Contains(guidance, "go test ./...") {
		t.Fatalf("Go guidance missing 'go test ./...': %q", guidance)
	}
	if !strings.Contains(guidance, "go mod tidy") {
		t.Fatalf("Go guidance missing 'go mod tidy': %q", guidance)
	}
	if strings.Contains(guidance, "npm install") {
		t.Fatalf("Go guidance should not contain npm: %q", guidance)
	}
	if strings.Contains(guidance, "python -m pytest") {
		t.Fatalf("Go guidance should not contain pytest: %q", guidance)
	}
}

func TestStackDeliveryPhaseGuidance_Python(t *testing.T) {
	guidance := stackDeliveryPhaseGuidance(StackPython)
	if !strings.Contains(guidance, "python -m pytest") {
		t.Fatalf("Python guidance missing pytest: %q", guidance)
	}
	if strings.Contains(guidance, "go test") {
		t.Fatalf("Python guidance should not contain go test: %q", guidance)
	}
	if strings.Contains(guidance, "npm install") {
		t.Fatalf("Python guidance should not contain npm: %q", guidance)
	}
}

func TestStackDeliveryPhaseGuidance_NodeJS(t *testing.T) {
	guidance := stackDeliveryPhaseGuidance(StackNodeJS)
	if !strings.Contains(guidance, "npm install --ignore-scripts") {
		t.Fatalf("Node guidance missing npm install: %q", guidance)
	}
	if !strings.Contains(guidance, "npx tsc --noEmit") {
		t.Fatalf("Node guidance missing tsc: %q", guidance)
	}
	if strings.Contains(guidance, "go test") {
		t.Fatalf("Node guidance should not contain go test: %q", guidance)
	}
	if strings.Contains(guidance, "python -m pytest") {
		t.Fatalf("Node guidance should not contain pytest: %q", guidance)
	}
}

func TestStackDeliveryPhaseGuidance_Docker(t *testing.T) {
	guidance := stackDeliveryPhaseGuidance(StackDocker)
	if !strings.Contains(guidance, "docker build") {
		t.Fatalf("Docker guidance missing docker build: %q", guidance)
	}
	if !strings.Contains(guidance, "docker-compose") {
		t.Fatalf("Docker guidance missing docker-compose: %q", guidance)
	}
}

func TestStackDeliveryPhaseGuidance_Generic_IncludesAll(t *testing.T) {
	guidance := stackDeliveryPhaseGuidance(StackGeneric)
	if !strings.Contains(guidance, "go test") {
		t.Fatalf("Generic guidance missing go test: %q", guidance)
	}
	if !strings.Contains(guidance, "python -m pytest") {
		t.Fatalf("Generic guidance missing pytest: %q", guidance)
	}
	if !strings.Contains(guidance, "npm install") {
		t.Fatalf("Generic guidance missing npm: %q", guidance)
	}
}

func TestSpecIndexSystemPromptIncludesStackGuidance(t *testing.T) {
	// Test that the base prompt includes the critical elements
	prompt := specIndexSystemPrompt()
	if !strings.Contains(prompt, "delivery_phases") {
		t.Fatalf("base prompt missing delivery_phases")
	}
	if !strings.Contains(prompt, "docker rmi test-app:latest") {
		t.Fatalf("base prompt missing docker cleanup")
	}
	if !strings.Contains(prompt, "go test ./...") {
		t.Fatalf("base prompt missing go test")
	}
	if !strings.Contains(prompt, "python -m pytest") {
		t.Fatalf("base prompt missing pytest")
	}
	if !strings.Contains(prompt, "npm install --ignore-scripts") {
		t.Fatalf("base prompt missing npm")
	}
}

func TestDetectStackFromSpec_EmptyDefaultsToGeneric(t *testing.T) {
	got := detectStackFromSpec("")
	if got != StackGeneric {
		t.Fatalf("empty spec: got %q want %q", got, StackGeneric)
	}
}