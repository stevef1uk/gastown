# PingApp – Trivial MVP spec (pipeline-friendly)

## Goal

A tiny Go application with a single HTTP endpoint. Success for the automated pipeline means:

```bash
cd pingapp && go mod tidy && go test ./...
```
passes, and `cd pingapp && go run main.go` serves the endpoint on `:8080`.

Keep implementations small and literal. No extra files or abstractions.

## Layout (implement beads only)

```
pingapp/
├── go.mod
├── main.go
└── main_test.go
```

## Module

```go
module pingapp

go 1.22
```

## HTTP API

Register on `http.DefaultServeMux` from `main`.

| Method | Path | Success |
|--------|------|---------|
| GET | `/ping` | 200, JSON `{"message": "pong"}` |

## `pingapp/main.go`

1. Define `func PingHandler(w http.ResponseWriter, r *http.Request)` that writes `{"message": "pong"}` with `Content-Type: application/json`.
2. In `main()`, register the handler to `/ping`.
3. Call `http.ListenAndServe(":8080", nil)` and log `listening on :8080`.

## `pingapp/main_test.go`

1. Define `func TestPing(t *testing.T)`.
2. Use `httptest.NewRecorder()` and `http.NewRequest("GET", "/ping", nil)`.
3. Call `PingHandler` directly.
4. Assert that the status code is 200 and the body matches `{"message": "pong"}`.

## Definition of done

1. `cd pingapp && go test ./...` — passes.
2. `cd pingapp && go run main.go & sleep 1 && curl http://localhost:8080/ping` — returns `{"message": "pong"}`.

**CRITICAL NOTE FOR AGENTS**: DO NOT run `go run main.go` or `./pingapp` in the foreground during implementation or testing. Because it is an HTTP server, it will block forever and cause you to hang indefinitely. Always run it in the background with `&` and test using `curl`, or rely on `go test`.
