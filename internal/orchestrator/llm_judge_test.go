package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/llm"
)

func TestLLMJudgeDockerSection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	archDoc := `# Architecture for test

## Docker & Deployment
This project uses a multi-stage Dockerfile.

### Stage 1: Node
FROM node:20-slim AS node_builder
WORKDIR /app
COPY frontend/ .
RUN npm ci && npm run build

### Stage 2: Python
FROM python:3.12-slim AS python_builder
ENV UV_SYSTEM_PYTHON=1
RUN pip install uv
WORKDIR /app
COPY backend/ .
COPY --from=node_builder /app/out ./static
RUN uv sync --no-dev
EXPOSE 8000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000"]

## Integration and testing
We use pytest for unit tests and Playwright for E2E.

## E2E / integration testing
The app is started with docker-compose -f test/docker-compose.test.yml up --build.
The Playwright service runs npx playwright test against http://app:8000.
Tests cover: homepage load, price streaming, watchlist CRUD, trade execution, portfolio chart, AI chat, SSE resilience.
Test data: LLM_MOCK=true forces deterministic LLM responses. SQLite is fresh per test run.
`

	cfg := JudgeConfig{
		DocumentName: "architecture.md",
		Content:      archDoc,
		Criteria: []string{
			"Documents the base images used in the Dockerfile (e.g., node:20-slim, python:3.12-slim)",
			"Describes the multi-stage build steps clearly",
			"Specifies the exposed port (8000)",
			"Documents the CMD/entrypoint used to run the server",
			"Describes how the app under test is started for E2E tests (docker-compose up)",
			"Describes how E2E tests are executed (Playwright command)",
			"Lists what the E2E tests cover (scenarios)",
			"Mentions test data/environment requirements (LLM_MOCK, fresh SQLite)",
		},
		MinLength: 500,
	}

	client := llm.NewClient(
		"http://localhost:11434/v1/chat/completions",
		"deepseek/deepseek-v4-flash",
		"",
		60*time.Second,
	)

	pass, reason, err := ValidateDocumentWithJudge(ctx, client, cfg)
	if err != nil {
		t.Fatalf("judge error: %v", err)
	}
	if !pass {
		t.Errorf("judge rejected valid doc: %s", reason)
	} else {
		t.Logf("judge passed: %s", reason)
	}
}