# Code Cache — Reusing Validated LLM Output

## Problem

The polecat generates the same kinds of files repeatedly across retries, guard rejections,
and phase boundaries. Each retry burns an LLM call to regenerate structurally identical code.
For example, when a test file is rejected by the Chdir guard, the LLM regenerates the entire
file with minor changes — wasting token budget on code that already exists in a known-good form.

## Design

A per-workflow, on-disk JSON cache keyed by `(phase_idx, file_path)`. Each entry stores the
file content, an MD5 checksum, and a `validated` flag set when the file passes post-write
verification.

### Cache Location

```
{rig}/mayor/rig/.gastown/code-cache/{workflow_id}.json
```

One file per workflow. Lives inside the rig's `.gastown/` metadata directory so it persists
across agent restarts and is naturally cleaned up when the rig is torn down.

### Entry Schema

```json
{
  "content": "package api\n...",
  "md5": "abc123def456",
  "validated": true,
  "validated_at": "2026-07-06T12:00:00Z",
  "created_at": "2026-07-06T11:59:00Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `content` | string | Full file content as written to disk |
| `md5` | string | MD5 hex digest of content for integrity check |
| `validated` | bool | True if post-write verification passed for this content |
| `validated_at` | string | RFC3339 timestamp of validation |
| `created_at` | string | RFC3339 timestamp of first cache write |

### Key

`"{phase_idx}:{file_path}"` where `phase_idx` is the delivery phase index and `file_path`
is the layout-relative file path (e.g. `2:internal/api/handlers.go`).

### API

```go
OpenCodeCache(rigDir, workflowID string) (*CodeCache, error)
Put(phaseIdx int, relPath, content string)
MarkValidated(phaseIdx int, relPath string)
GetValidated(phaseIdx int, relPath string) (string, bool)
GetAny(phaseIdx int, relPath string) (string, bool)
ClearPhase(phaseIdx int)
InsertCachedContentIntoPrompt(prompt string, phaseIdx int, relPaths []string, cache *CodeCache) string
```

## How It Avoids LLM Calls

### 1. Post-write verification caches validated content

After `runPostNativeWriteVerify` succeeds in `implement_post_write.go:133`,
`cacheValidatedContent(relPath)` stores the file and marks it validated.
This happens automatically — no configuration needed.

### 2. Guard-rejection auto-restore (future)

When a WRITE/EDIT is rejected by a guard (Chdir, stub handler, etc.), the runner checks
the cache for a validated version. If found, it restores the cached content directly and
re-runs verification — bypassing the LLM entirely. This eliminates the retry loop shown in
the logs where the LLM regenerates the same test file 3-4 times.

### 3. Prompt hints for the LLM

`InsertCachedContentIntoPrompt()` checks which required files for the current bead have
validated cache entries. It appends a block to the LLM prompt:

```
## Cached validated content available

The following files already have validated content in the code cache.
Prefer reusing them over regenerating:
  - internal/api/handlers.go (validated — reuse existing content)
  - internal/api/handlers_test.go (validated — reuse existing content)
```

This reduces LLM output tokens by steering the model away from regenerating known-good files.

### 4. Cross-phase reuse

If the same file path appears in multiple delivery phases (e.g. `web/app.js` in both
web-static and web-shell), the validated entry from the earlier phase is still in the
cache when the later phase starts. The prompt hint tells the LLM to reuse it.

## Performance Notes

- Cache file is loaded once at `OpenCodeCache` and saved on each mutation.
- File is small (dozens of entries at most for typical rigs).
- MD5 is computed for integrity — not used for dedup (LLM generates semantically
  equivalent but byte-different code even for the same intent).
- No TTL. Entries are cleared by phase via `ClearPhase()` when a phase is replayed.

## Future Work

- **Guard-rejection restore**: wire `restoreCachedContent` into the rejection path in
  `processOrchestratedTools` so rejected writes auto-restore from cache.
- **Redis backend**: swap the file-based store for Redis by implementing the same
  interface (the user offered to run Redis in Docker).
- **Cross-workflow reuse**: key by architecture hash so identical specs reuse cached
  content across workflow runs.
- **Cache invalidation**: when architecture.md or SPEC.md changes, invalidate
  cache entries for affected files.
