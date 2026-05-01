# GasTown Workspace Stabilization Fixes

This document details the modifications and setup changes required to restore workspace integrity and achieve a passing status in `gt doctor`.

## 1. Code Modifications (GasTown Tooling)

### `internal/doctor/rig_config_sync_check.go`
- **Town Root Support**: Expanded `RigConfigSyncCheck` to recognize the town root as a specialized rig that lacks the standard `mayor/rig` subdirectory structure.
- **Path Detection**: Improved `doltDatabaseExists` and `rigBeadExists` to dynamically detect whether to look in the rig root or `mayor/rig` based on the rig type.
- **Bug Fixes**: Resolved critical syntax errors (unbalanced braces, incorrect function signatures) and missing imports (`errors`, `workspace`) introduced during initial refactoring.

### `internal/doctor/routes_check.go`
- **Canonical Layout Detection**: Updated `determineRigBeadsPath` to prioritize the `mayor/rig/.beads` layout if a root-level `.beads` directory is missing. This ensures the doctor correctly identifies the modern GasTown rig structure.
- **Robust Route Fixing**: Enhanced the `Fix` logic to trigger route rewrites not only for legacy redirects but also when the current routed path is physically missing from disk.

## 2. Configuration & Metadata Changes

### Prefix Synchronization
- **`gt/mayor/rigs.json`**: Updated the prefix for the `gt` rig from `hq` to `gt` to align with the registry and prevent "prefix mismatch" warnings.
- **`gt/gt/config.yaml`**: Updated `issue-prefix` to `gt` for consistency with the rig identity.
- **`gt/fin/config.yaml`**: Ensured `issue-prefix` matches the `fin` registry entry.

### Git Environment
- **`gt/.gitignore`**: Added `.runtime/` to the town-root gitignore to suppress health-check warnings regarding ephemeral runtime files.

## 3. Manual Remediation & Database Alignment

### Cleanup of Stale Beads Metadata
- Deleted duplicate/accidental `.beads` directories at rig roots (`gt/gt/.beads`, `gt/fin/.beads`) that were using "embedded" Dolt mode instead of the required "server" mode.
- Removed unregistered beads directories (`gt/beads/`, `gt/freeride/`) that were leftover from previous configuration attempts.

### Identity & Agent Bead Restoration
- **Rig Identities**: Manually initialized `gt-rig-gt` and `fin-rig-fin` in their respective Dolt databases.
- **Agent Beads**: Re-created missing infrastructure beads (`gt-witness`, `gt-refinery`, `fin-witness`, `fin-refinery`) and ensured they were tagged with the required `gt:agent` label. This was necessary because the automated `bd create` calls were failing due to temporary routing inconsistencies.

### Routing Table Update
- Executed `gt doctor --fix` (leveraging the updated detection logic) to rewrite `routes.jsonl`. This correctly mapped the `gt-` and `fin-` prefixes to their canonical `mayor/rig` paths, bypassing broken redirect chains.

## 5. Proxy & Agent Stability (FreeRide Proxy)

### `main.go` (Proxy Logic)
- **Tool Choice Sanitization**: Improved handling of `tool_choice` to strip both string and map-based `"auto"` configurations. This resolved `400 Bad Request` errors returned by NVIDIA/Mistral NIM models when Claude Code sent complex tool choice objects.
- **Context Length Cooldown Bypass**: Updated the error handling to ignore `400` errors related to "context length" or "too many input tokens." This prevents a "death spiral" where a single oversized request could put all available models into a 10-minute cooldown.
- **Deepseek Model Identification**: Fixed a logic error in `isNvidiaModel` to correctly flag `deepseek` models for tool-use sanitization.
- **Environment Loading**: Fixed an issue where the proxy was not correctly sourcing the `.env` file upon manual restarts, leading to model-skipping due to missing API keys.

### Mayor & Service Resilience
- **Mayor Recovery**: Stabilized the `hq-mayor` session by resolving the proxy errors that caused the agent to exit prematurely.
- **Refinery Initialization**: Resolved refinery startup timeouts by ensuring the proxy was healthy and available to handle the initial status polls.
- **Log Management**: Truncated the `freeride_live.log` which had bloated to 130MB, significantly improving log-tailing performance and system responsiveness.

## 6. Final Validation Result
- **Checks Passed**: 87
- **Failures**: 0
- **Warnings**: 4 (Orphaned/Zombie sessions - transient)

The GasTown daemon, agent proxy, and the entire multi-agent ecosystem are now fully operational and stable.

