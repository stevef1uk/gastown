# Design Document: The Gas Town Mechanic (Repair Agent)

## 1. Objective
The **Mechanic** is a specialized meta-agent designed to ensure "Total System Throughput" by autonomously detecting and repairing agent hallucinations, environmental mismatches, and configuration drift. It functions as the "automated debugger" for the Gas Town ecosystem.

## 2. The Problem: Hallucination Stalls
Agents (especially those using smaller or less reasoning-heavy models) often fail due to:
- **Path Hallucination**: Guessing a file path (e.g., `attached_molecule/spec.md`) that doesn't exist.
- **Case Sensitivity**: Trying to read `spec.md` when the file is `SPEC.md`.
- **Configuration Drift**: Prefix mismatches between the Town and Rig databases.
- **Directory Displacement**: Running git commands in a non-git directory.

Currently, these issues require human intervention to "shim" the environment or nudge the agent.

## 3. Role Overview: The Mechanic
The Mechanic does not implement features or design architecture. Instead, it **patrols the logs** of other agents.

### 3.1 Responsibilities
- **Log Surveillance**: Monitors `{{ .TownRoot }}/logs/sessions/*.log` and rig-level `typescript` files.
- **Pattern Recognition**: Detects "Extraordinary action" warnings and repeated `exit status 1` failures.
- **Automated Shimming**: Creates symlinks, directories, and files to satisfy an agent's hallucination if the intent is clear.
- **Config Repair**: Automatically aligns `.beads/config.yaml` and `.beads/redirect` files.
- **Corrective Nudging**: Sends `gt nudge` commands to agents to override incorrect memories.

## 4. Technical Architecture

### 4.1 Detection Logic
The Mechanic identifies a "Service Event" when:
1. A log entry shows `Extraordinary action detected (retry #3+)`.
2. A command output shows `No such file or directory` followed immediately by the same command in the next cycle.
3. A `bd` command fails with a `prefix mismatch` error.

### 4.2 Remediation Toolkit
The Mechanic must be granted elevated permissions to touch other agents' workspaces:
- **`shimmer`**: A tool to create symlinks from a hallucinated path to a real path.
- **`aligner`**: A tool to sync beads configurations across rigs.
- **`primer`**: A tool to force-refresh an agent's system prompt with updated context.

## 5. Configuration into Gas Town

### 5.1 Role Registration
The Mechanic will be added to the core `gt-agent` binary as a functional role.
- **Role Name**: `mechanic`
- **Scope**: Town-level (singleton).

### 5.2 Template Definition
A new template `internal/templates/roles/mechanic.md.tmpl` will define its "patrol formula":
1. `ls -rt {{ .TownRoot }}/logs/sessions/` (find recently active logs)
2. `tail -n 50 <log-file>` (check for errors)
3. `if error detected -> analyze + fix`
4. `report fix to Mayor/User`

### 5.3 Activation
The Mechanic is started at the Town level:
```bash
gt up  # Automatically starts the mechanic if configured in town-root/config.json
```

## 6. Success Metrics
- **Mean Time to Recovery (MTTR)**: Reduction in time between an agent's first hallucination and its first successful command.
- **Autonomy Rate**: Percentage of agent stalls resolved without human `gt nudge`.

## 7. Safety & Guardrails
- **No Code Modification**: The Mechanic may create symlinks and configs, but NEVER modifies project source code.
- **Traceability**: Every "shim" created by the Mechanic must be logged in the `Capability Ledger`.
- **Escalation**: If a repair fails 3 times, the Mechanic must email the Mayor and User for manual intervention.
