# SECURITY_STANDARDS.md — Security Standards for Gas Town Rigs

This file defines the security standards enforced by gt-agent's deterministic
secret-scanner (see `internal/orchestrator/security_scan.go`). It is embedded
into every new gt town on install and read by QA agents at runtime.

## Philosophy

- **Never commit real credentials.** All secrets must be stored outside the
  repository (environment variables, secret managers, vaults). Never hard-code
  API keys, private tokens, or connection strings in source.
- **Fixtures use reserved domains/numbers.** Example data must use reserved blocks:
  - Emails: `example.com`, `example.net`, `example.org`, `invalid`, `localhost`
  - Phone numbers: `555-01xx` (NANP fiction block — cannot dial a real person)
  - API keys: `akia_` placeholder or documented fake keys
- **Escape hatch.** If a genuine test fixture requires a credential-like string,
  add `allow-secret: <reason>` on the same line. The reason must be present;
  a bare `allow-secret` marker without a reason is also accepted (bench-style
  convention) but discouraged — prefer an explicit reason.
- **Deterministic gt-agent enforcement.** gt-agent runs a secret-scan on every
  QA `all_passed`/`task_passed` outcome. Findings block success until resolved.
- **Optional local gate.** Run `gitleaks dir . --no-banner --redact` locally as
  an additional check. The gt-agent scan is the CI gate; gitleaks is a local
  developer backstop.

## What gt-agent Scans For

gt-agent's deterministic secret scanner checks the rig's layout root for the
following high-signal patterns (bench-inspired):

| Kind            | Pattern (regex)                                                                    |
|-----------------|------------------------------------------------------------------------------------|
| `private_key`   | `-----BEGIN ... PRIVATE KEY-----`                                                  |
| `aws_key`       | `\bAKIA[0-9A-Z]{16}\b` (AWS access key ID)                                       |
| `github_token`  | `gh[pousr]_[A-Za-z0-9]{36}` (classic GitHub token format)                         |
| `slack_token`   | `xox[baprs]-[A-Za-z0-9-]{10,}` (Slack bot/user token format)                     |
| `google_key`    | `AIza[0-9A-Za-z_\-]{35}` (Google API Key format)                                  |
| `jwt`           | `\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}\b` (JWT format) |
| `secret_assign` | `(?i)(api[_-]?key|secret|password|passwd|token|auth[_-]?token|access[_-]?token)\s*[:=]\s*["'][^"'{]{8,}["']` |

## What gt-agent Skips (false-positive prevention)

- Lines containing `allow-secret: <reason>` — the whole line is excluded from
  all checks.
- Lines containing obviously fictional 555-phone numbers (area code 555 with
  exchange 01).
- Placeholder prefixes/values: `your-`, `your_`, `example`, `changeme`, `<...>`,
  `${...}`, `process.env`, `os.Getenv`, `xxx`, `your-api-key`.
- Binary files, files > 1 MB, and skip directories (`.git`, `node_modules`,
  `vendor`, `.venv`, `venv`, `__pycache__`, `dist`, `build`).
- Lockfiles (`go.sum`, `*-lock.json`) and module declarations (`go.mod`).

## Reporting Format

When gt-agent finds findings, the QA `all_passed`/`task_passed` outcome is
rejected with a message formatted by `FormatSecretFindings()`, e.g.:

```
Security validation found potential secrets/credentials:
  aws_key (rig/mayor/rig/cmd/server/main.go:42): AKIA0123456789ABCDEF
  secret_assign (rig/mayor/rig/config.yaml:17): api_key="my-secret-key-123"

To allow a secret, add `allow-secret: <reason>` on the same line.
Refer to SECURITY_STANDARDS.md for full standards.
```

## Complementary Local Checks

For defense-in-depth, run `gitleaks dir . --no-banner --redact` on your local
machine. The gt-agent scan is the CI gate; gitleaks is a developer backstop.
Both use largely overlapping but not identical pattern sets.

## Version

This file is versioned alongside the gt binary. On reinstall, the embedded copy
is refreshed from the gastown repository. Edits to `~/.gt/orchestrator/SECURITY_STANDARDS.md`
are overwritten on reinstall — make changes in the gastown source and rebuild.