# Architecture for pwtest

## Overview
This architecture implements the SPEC goal of a tiny Go 1.22 HTTP server with one endpoint and a static browser page. The layout_root is pingapp. The Go application is a single host process, not a Docker service. It listens on host port 8080, serves the static page from the root URL, and exposes GET /ping with HTTP 200 and the exact JSON response {"message":"pong"}. Playwright is an external verification harness running in Docker Compose and reaches the host process through host.docker.internal.

The design intentionally has no database, persistence store, migrations, authentication layer, Dockerfile, or Go application image. It keeps the four SPEC milestones bounded: module initialization, server behavior, static UI behavior, and the Playwright harness. Repository-root verification commands preserve this topology: the Go server runs directly on the host, while Compose runs only the Playwright harness.

## Planned file layout
Every file required by SPEC is assigned to exactly one delivery phase:
- `pingapp/go.mod` — Go 1.22 module manifest.
- `pingapp/cmd/server/main.go` — host-run HTTP entrypoint, route handlers, and static file serving.
- `pingapp/web/index.html` — the single page with visible Hello text and a button.
- `pingapp/web/app.js` — browser click behavior that changes visible text.
- `pingapp/package.json` — npm manifest and the static frontend production-build script.
- `pingapp/playwright.config.ts` — Playwright configuration for the host URL.
- `pingapp/e2e/ping.spec.ts` — browser assertions for the endpoint and UI.
- `pingapp/docker-compose.yml` — only the Playwright test-harness service; it must not define a Go service or application image.

No other implementation file is required by SPEC. Generated npm lockfiles, if produced by npm, are tool outputs rather than hand-authored delivery files.

## Module / bead ownership
The implementation is deliberately small and has no store API. The ownership boundary is:
| File | Owns | Must not define |
| --- | --- | --- |
| `pingapp/go.mod` | Go module identity and Go 1.22 language version | Runtime handlers |
| `pingapp/cmd/server/main.go` | package main, host server startup, GET /ping handler, static serving | Browser markup, browser event logic, Docker configuration |
| `pingapp/web/index.html` | Document structure, Hello text, button element, script reference | Go handlers or inline server behavior |
| `pingapp/web/app.js` | DOM click listener and visible text transition | HTTP server startup or Docker behavior |
| `pingapp/package.json` | npm scripts and Playwright dependency metadata | Go code or runtime routes |
| `pingapp/playwright.config.ts` | Browser base URL and test execution settings | Application startup or application implementation |
| `pingapp/e2e/ping.spec.ts` | End-to-end assertions | Production server code |
| `pingapp/docker-compose.yml` | Playwright harness container and host connectivity | Go application service, Dockerfile, database |

There are no exported Go symbols required by SPEC. The entrypoint may use unexported handler functions or direct standard-library registration; either shape must preserve the stated HTTP contract.

## HTTP + entrypoint integration
`pingapp/cmd/server/main.go` starts the host process on port 8080. It registers GET /ping and returns status 200, an application/json content type, and the exact JSON object {"message":"pong"}. The handler must not introduce a database or a store dependency. The same entrypoint serves `pingapp/web/index.html` at / and makes `pingapp/web/app.js` reachable as the page's script asset. Static requests and the API route must remain distinct so the API always returns JSON.

The normal host command is go run `pingapp/cmd/server/main.go`, issued from the repository root. The application is ready before the Compose harness starts. The server is not built into or launched by `pingapp/docker-compose.yml`.

## Unit tests
The core verification uses the SPEC command go test ./... from the Go module directory and must compile the delivered server. Where the implementation adds Go tests, they should use the standard library and verify the handler's status, content type, and exact JSON response without a database. The absence of a separate Go test file in the required layout is intentional: SPEC names only `pingapp/cmd/server/main.go` for the core phase.

The static frontend has no server-side package boundary. Its executable acceptance evidence is supplied by the production build command in `pingapp/package.json` and by the browser assertions in `pingapp/e2e/ping.spec.ts`.

## Integration and testing
The integration flow runs from the repository root. It starts `pingapp/cmd/server/main.go` on the host, waits briefly for port 8080, runs the Compose Playwright service, captures its exit code, and terminates the host process without replacing the test failure with cleanup status. The Playwright browser uses http://host.docker.internal:8080 as its base URL.

The E2E assertions must demonstrate behavior rather than file existence: GET /ping is HTTP 200 with the required JSON message; the root page visibly contains Hello and a button; clicking the button changes the visible text. The Compose project contains only the Playwright harness and does not start, build, or network a Go container.

## Docker & Deployment
Docker is limited to `pingapp/docker-compose.yml` and the Playwright verification container. The Go server runs directly on the host and has no Dockerfile or application image. The harness must provide host.docker.internal resolution so the browser can reach port 8080. Compose exit-code propagation is part of the integration contract, and cleanup must occur even when Playwright fails. No database volume, database path, or persistent deployment resource is designed.

The integration verification must not run docker compose build --no-cache, docker rmi test-app:latest, or docker image prune -f. This design has no Dockerfile, application image, or named test-app image. It must not prepend cd pingapp to commands that already use repository-root paths.

## E2E / integration testing
`pingapp/playwright.config.ts` targets http://host.docker.internal:8080 and loads tests from `pingapp/e2e/ping.spec.ts`. The test file covers the API response, the root page's Hello text and button, and the button click transition. `pingapp/docker-compose.yml` runs the Playwright test command in its sole harness service. The host server is started outside Compose by the delivery verification command, so the harness remains faithful to the SPEC topology.

## Acceptance mapping
The go-module phase proves that `pingapp/go.mod` declares a Go 1.22 module and supports go test ./.... The core phase proves the host server starts on port 8080, serves GET /ping with the required 200 JSON response, and serves the static root without adding persistence or a container.

The web phase proves the exact static files exist, the page has Hello and a button, the JavaScript changes visible text after a click, and `pingapp/package.json` provides a successful production build. The integration-test phase proves the host-only server and Docker-only Playwright harness work together through host.docker.internal. These gates collectively cover every goal, route, UI behavior, required file, and deployment constraint in SPEC.

## Requirements
### go-module
Initialize `pingapp/go.mod` for Go 1.22. The phase is complete when the module is valid and go test ./... succeeds from the module directory; it does not add application behavior or infrastructure.

### core
Implement `pingapp/cmd/server/main.go` as the host-run single-file Go server. The phase must provide GET /ping with HTTP 200 and JSON {"message":"pong"} on port 8080, while also wiring static serving for the root page. It must not require Docker, a database, or a persistence path.

### web
Implement `pingapp/web/index.html`, `pingapp/web/app.js`, and `pingapp/package.json`. The page must visibly show Hello and a button, and clicking the button must change visible text. The npm manifest must expose a production build command suitable for the static assets; the phase gate must run that build rather than relying only on file existence.

### integration-test
Implement `pingapp/playwright.config.ts`, `pingapp/e2e/ping.spec.ts`, and `pingapp/docker-compose.yml`. The browser must verify the API and UI against the host server at host.docker.internal:8080. Compose must run only the Playwright harness, while the Go server remains on the host. Its repository-root verification command starts the host process, invokes docker compose -f pingapp/docker-compose.yml up --exit-code-from playwright, preserves that exit code through cleanup, and omits application-image build and image-pruning steps.

## Delivery phases
This YAML block is the sole canonical delivery contract for workflow-profile synchronization. It contains exactly the four SPEC phase IDs, assigns every SPEC file exactly once, and uses concrete literal paths. Every command runs from the repository root. The integration command deliberately uses the host-run Go process and only the Playwright Compose service; synchronization must retain it without adding cd pingapp, an application-image build, docker rmi test-app:latest, or docker image prune -f.
```yaml
delivery_phases:
  - id: go-module
    title: Go Module
    spec_focus: Go Module
    required_files:
      - pingapp/go.mod
    qa_verify_command: 'test -f pingapp/go.mod && cd pingapp && go mod tidy && go test ./...'
    depends_on: []
  - id: core
    title: Backend Server
    spec_focus: Backend Server
    required_files:
      - pingapp/cmd/server/main.go
    qa_verify_command: 'sh -c "go run pingapp/cmd/server/main.go >/tmp/pingapp-server.log 2>&1 & pid=$!; trap \"kill $pid 2>/dev/null || true\" EXIT; sleep 2; test \"$(curl -s -o /tmp/pingapp-ping.json -w \"%{http_code}\" http://127.0.0.1:8080/ping)\" = 200; grep -q \"pong\" /tmp/pingapp-ping.json; test -f pingapp/cmd/server/main.go"'
    depends_on:
      - go-module
  - id: web
    title: Frontend Web Application
    spec_focus: Frontend Web Application
    required_files:
      - pingapp/web/index.html
      - pingapp/web/app.js
      - pingapp/package.json
    qa_verify_command: 'npm --prefix pingapp install && npm --prefix pingapp run build && test -f pingapp/web/index.html && test -f pingapp/web/app.js'
    depends_on:
      - core
  - id: integration-test
    title: E2E Testing
    spec_focus: E2E Testing
    required_files:
      - pingapp/playwright.config.ts
      - pingapp/e2e/ping.spec.ts
      - pingapp/docker-compose.yml
    qa_verify_command: 'sh -c "go run pingapp/cmd/server/main.go >/tmp/pingapp-server.log 2>&1 & pid=$!; trap \"kill $pid 2>/dev/null || true\" EXIT; sleep 2; docker compose -f pingapp/docker-compose.yml up --exit-code-from playwright; rc=$?; kill $pid 2>/dev/null || true; wait $pid 2>/dev/null || true; exit $rc"'
    depends_on:
      - web
