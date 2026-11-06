# Ping App - Trivial Go Web Server

## Goal
A tiny Go HTTP server with one endpoint. Tests run via Playwright in a Docker compose wrapper — the Go server itself runs on the host and is NOT containerized.

## Architecture
- Go 1.22, single main.go
- Static HTML/JS served from /
- **Go server runs on the HOST directly (go run pingapp/cmd/server). No Docker is needed for the Go application.**
- Playwright E2E tests run in Docker compose, reaching the host through host.docker.internal

## HTTP API
- GET /ping → 200 JSON {"message": "pong"}

## Web UI
- Single page at / with "Hello" and a button
- Button click changes text

## Delivery Phases
1. **go-module** - Initialize go.mod
2. **core** - main.go with /ping handler
3. **web** - index.html + app.js
4. **integration-test** - Playwright E2E via Docker compose (Playwright container reaches host via host.docker.internal; Go server stays on the host)

## Layout
**layout_root: pingapp**

```
pingapp/
├── go.mod
├── cmd/server/main.go
├── web/index.html
├── web/app.js
├── playwright.config.ts
├── e2e/ping.spec.ts
├── package.json
└── docker-compose.yml
```

## Testing
- Unit: `go test ./...`
- E2E: `docker compose up --exit-code-from playwright` (docker-compose.yml runs ONLY the Playwright test harness container; the Go server is not containerized)
- The Go server runs on the host on port 8080. Do NOT define a Dockerfile or base image for the Go application — Docker is only the Playwright test harness.
