# Bounded workflow execution and recovery

Delivery phases describe SPEC milestones. `hooks.execution_batch_size` limits the
number of closed beads processed by one implementation session (rig-flow default:
8). After that many closures the agent reports a checkpoint timeout, retains
closed beads, and resumes the remaining queue in a fresh session. The original
milestone acceptance gate runs when its implementation queue is complete; it is
not copied onto incomplete file chunks. Explicit delivery contracts retain their
phase IDs and acceptance commands.

The 100-turn session limit is independent of the durable per-bead attempt limit
(default 40 for new progress files). Neither limit deletes source files. Review
and planning round trips preserve progress; a new workflow or delivery phase
gets its own checkpoint scope. Counter repairs preserve all other checkpoint
fields, including unknown future fields. File existence alone never creates a
successful verification checkpoint.

## Request admission

Every agent HTTP attempt, including a retry, reserves against
`{town_root}/orchestrator/execution-budgets.json`. A cross-process lock and atomic
rename serialize concurrent agents. Defaults:

| Setting | Default |
| --- | --- |
| `GT_WORKFLOW_MAX_LLM_REQUESTS` | 1000 |
| `GT_WORKFLOW_MAX_LLM_TOKENS` | 20000000 |

The token reservation is serialized input bytes plus the explicit 8192-token
output allowance. Reservations are conservative and are not refunded after a
failure: the upstream may already have generated a billable response. Invalid,
missing-identity, unreadable or corrupt accounting fails closed. Limits must be
positive integers. Billing/authentication errors and exhausted admission budgets
pause the workflow rather than polling the paid endpoint indefinitely.

The HTTP client owns retries, avoiding a second nested retry loop. Temporary 429
and server failures use exponential backoff and bounded Retry-After. Exhausted
credits and insufficient-quota responses are not transient. Responses log actual
model, usage, reported cost and workflow/rig identity when the provider supplies
those fields. Missing reported cost is not treated as proof of a free response.

## Recovery and toolchains

Stuck detection measures time since the most recent bead/source fingerprint
change, rather than the age of the entire workflow state. State changes start a
new progress window. Process cleanup requires a listener's working directory to
be within the exact rig worktree; a matching port or executable name is not
sufficient ownership. Unknown owners are left running, and shared town ports are
excluded. The standalone port-cleanup utility remains an explicit administrative
tool; automatic recovery no longer invokes its global process patterns.

Package manifests identify independent component roots. Multiple Go modules,
Python projects and Node packages get separate setup commands and their own
working directories. Python environment handling is no longer disabled merely
because Go is present. Full milestone verification remains the declared contract;
component setup does not establish behavioural acceptance.

## Installation and existing runs

Rebuild/install gt-agent and gt, then sync embedded templates into each town with
`gt orchestrator sync --update-changed` and restart the orchestrator/agents using
the normal maintenance procedure. Rebuild/restart FreeRide as well to enable its
upstream request and cost controls. Existing explicit profiles need not be split
or have their milestone acceptance gates weakened. Already-open beads are not
assumed verified; the workflow reconciles their on-disk evidence.

After a budget pause, inspect the failure and ledger, correct credentials or
raise a deliberately chosen allowance, then use `gt mayor workflow resume` for
the same instance. Do not delete accounting while workers are running. A resume
never silently resets an allowance. No deployment to another computer is part
of the source change.

The patch improves durable bounded recovery; it does not promise that an LLM can
solve every arbitrary task. Versioned structured acceptance commands, full
component-environment adapters and transactional multi-store/worker-lease
migrations remain separate architectural work. Existing profile validation now
also rejects duplicate phase IDs, unknown dependencies and dependency cycles.

## Local test reliability

`make test` runs the complete default suite with two package workers. Set
`TEST_PACKAGE_PARALLELISM` to override that limit. Cross-platform build tests still
cover all six supported OS/architecture combinations, but run at most two builds
at once with bounded compiler parallelism. This avoids multiplying compiler and
linker memory pressure on developer machines.

Live Beads/Dolt fixtures require the existing `GT_DOLT_TESTS=1` opt-in, as set by
`make test-integration`. Installing `bd` alone no longer changes which database
fixtures run in the unit target. Their assertions remain available in integration
runs. Unit tests for absent sessions use provider stubs, and worktree unit tests
control disk admission rather than relying on the host's current free space.
The production disk-space guard remains enabled; its rejection now also releases
an allocated polecat name and pending reservation.

The shell fixtures use portable in-place editing, compare failed `cd` against the
actual shell's exit status, and wait for startup diagnostics instead of assuming
50 ms is enough. Environment-creation tests select the interpreter they checked.
Production Python venv creation has a one-minute bootstrap deadline, and callers
can use `EnsureVenvContext` for a stricter deadline; a stalled shim cannot block
workflow environment preparation indefinitely.
