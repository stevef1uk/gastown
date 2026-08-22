# Docker E2E Testing Design

## Overview

Gas Town supports three Docker deployment patterns for Playwright E2E testing,
determined by the rig's workflow profile and required files.

## Deployment Patterns

### 1. Host-Run (`host-run`)

Server runs on HOST. Playwright runs in Docker, connects via `host.docker.internal`.

**Detection:** Root `docker-compose.yml` exists but no non-default `Dockerfile.*`.

```
  HOST
  +-----------------+         +-----------------+
  | Go/Python/Node  | <------ | Playwright      |
  | server :PORT    | host    | (Docker, volume |
  +-----------------+ .docker + mounts)         |
                       internal +-----------------+
```

**Template:** `docker-compose.host-run.yml`  
**QA Flow:** Kill port -> start server & -> sleep 3 -> docker-compose up -> kill server

### 2. Single-Container (`single-container`)

App runs in Docker container. Playwright in separate container, internal network.

**Detection:** `test/docker-compose.test.yml` exists.

```
  Docker Network
  +-------------+    +-------------+
  | app         |    | playwright  |
  | (server)    |<---| (tests)     |
  +-------------+    +-------------+
```

**Template:** `docker-compose.single-container.yml`  
**QA Flow:** docker-compose up (compose handles startup via depends_on + healthcheck)

### 3. Multi-Service (`multi-service`)

Multiple services (backend + frontend + DB). All in Docker.

**Detection:** Root `docker-compose.yml` + non-default `Dockerfile.*`.

```
  Docker Network
  +----------+  +----------+  +-------+
  | backend  |  | frontend |  | db    |
  +----------+  +----------+  +-------+
       ^              ^
       +------+-------+
              |
       +-------------+
       | playwright  |
       +-------------+
```

**Template:** `docker-compose.multi-service.yml`  
**QA Flow:** docker-compose up (compose handles all services)

## Template Files

| Template | Used By | Purpose |
|----------|---------|---------|
| `Dockerfile.playwright` | All | Shared runner image (built once) |
| `Dockerfile.playwright-stage` | All | Per-rig image with npm deps |
| `docker-compose.host-run.yml` | Host-run | Server on host, Playwright in Docker |
| `docker-compose.single-container.yml` | Single | App + Playwright in Docker |
| `docker-compose.multi-service.yml` | Multi | Multiple services + Playwright |

## Volume Mount Strategy

All templates mount source files:

- `.:/src:ro` - Host source tree (read-only, prevents modification)
- `playwright-app:/app` - Writable volume for test results

At startup: `cp -r /src/* /app/` copies source to writable volume.

## Server Command Detection

`devServerCommandForQA()` detects the server command:

| Stack | Command |
|-------|---------|
| Go | `go run ./cmd/server` |
| Python (uvicorn) | `python3 -m uvicorn <module>:app --host 0.0.0.0 --port PORT` |
| Python (flask) | `python3 -m flask run --host 0.0.0.0 --port PORT` |
| Node | `npm run dev` |
| Static | (none) |

## Port Cleanup

`KillTCPListenersOnPort()` frees ports via `fuser -k` or `lsof + kill`.
Protected ports (NATS, Dolt) are never killed.

## Key Files

| File | Path |
|------|------|
| Templates | `internal/orchestrator/town/templates/rig-init/` |
| Scaffold | `internal/orchestrator/rig_scaffold.go` |
| QA prompt | `internal/orchestrator/rig_flow_qa_prompt.go` |
| Server detect | `internal/orchestrator/dev_server_command.go` |
| Port mgmt | `internal/orchestrator/dev_server_ports.go` |
| Kind detect | `rig_scaffold.go:resolveComposeKind()` |
