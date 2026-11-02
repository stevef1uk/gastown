## Town Hall Coding Standards (Go)

- **Package & entrypoint discipline:** Keep a single `package main` entry point (e.g. `cmd/server/main.go`); put domain/store/handler packages under `internal/`. Never redefine a type across files in the same package.
- **Error handling & context:** Return explicit `error` values (`fmt.Errorf("...: %w", err)`). Pass `context.Context` as the first parameter on I/O and server calls; never `panic` in request handlers.
- **Concurrency & state:** Guard in-memory maps/slices with `sync.RWMutex` or `sync.Mutex`. Initialize slices to `[]T{}` when JSON responses require `[]` instead of `null`.
- **Testing:** Use table-driven tests in `*_test.go` with `t.Run` and `httptest.NewRequest` / `httptest.NewRecorder` for HTTP handlers. No `init()` side effects.
