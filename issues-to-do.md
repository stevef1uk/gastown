## Summary: QA, Planner, and Architect Roles Analysis

I've completed a thorough exploration of the three new agent roles in the `pr/qa-features` branch. Here's my detailed report:

## Implementation Progress
- [x] Add explicit QA defect workflow support in `gastown/internal/cmd/qa.go`
- [x] Add QA approval notification support to Refinery in `gastown/internal/cmd/qa.go`
- [x] Add explicit QA review command and approval label tracking in `gastown/internal/cmd/qa.go`
- [x] Strengthen QA acceptance criteria guidance in `gastown/internal/templates/roles/qa.md.tmpl`
- [x] Add Planner wait-for-Architect guidance and spec reference requirements in `gastown/internal/templates/roles/planner.md.tmpl`
- [x] Add ADR storage guidance to `gastown/internal/templates/roles/architect.md.tmpl`
- [ ] Add Planner/Architect readiness enforcement beyond documentation
- [ ] Add unified progress status bead concept
- [ ] Add mandatory code coverage/security threshold enforcement for QA

---

## ✅ Implementation Status

All three roles are **fully implemented** with CLI commands, templates, and configuration:

| Role | Scope | Files | Status |
|------|-------|-------|--------|
| **QA** | Per-rig | [qa.go](gastown/internal/cmd/qa.go), [qa.md.tmpl](gastown/internal/templates/roles/qa.md.tmpl), [qa.toml](gastown/internal/config/roles/qa.toml) | ✅ Complete |
| **Planner** | Town-level | [planner.go](gastown/internal/cmd/planner.go), [planner.md.tmpl](gastown/internal/templates/roles/planner.md.tmpl), [planner.toml](gastown/internal/config/roles/planner.toml) | ✅ Complete |
| **Architect** | Per-rig | [architect.go](gastown/internal/cmd/architect.go), [architect.md.tmpl](gastown/internal/templates/roles/architect.md.tmpl), [architect.toml](gastown/internal/config/roles/architect.toml) | ✅ Complete |

**Session naming** properly configured in [session/names.go](gastown/internal/session/names.go):
- Planner: `hq-planner` (town-level singleton)
- QA: `{rig}-qa` (per-rig, e.g., `gt-qa`)
- Architect: `{rig}-architect` (per-rig, e.g., `gt-architect`)

**Startup integration** working in [up.go](gastown/internal/cmd/up.go#L333-L340):
- Planner started at line ~333 (before rig-level agents)
- QA and Architect started concurrently in worker pool at lines ~810-820

---

## 🔴 Critical Gaps Preventing Quality Software Development

### 1. **No Explicit Workflow Handoffs**
**Problem**: Roles operate independently with no documented coordination.

**Evidence**: 
- QA template references "Refinery merge queue" but no contract defined for message format
- Planner template says "consult Architect" but no mechanism specified
- Tasks aren't linked to architecture decisions or original specs
- No traceability from task → implementation → QA review → merge

**Impact**: A feature could pass through the entire pipeline without anyone verifying it matches the original specification.

**Example**: Mayor receives SPEC, tells Architect to design, who tells Planner to plan, who creates tasks, but tasks don't reference the SPEC. QA reviews code against "standards" but never validates it solves the original problem.

---

### 2. **QA Has No Execution Methods for Core Responsibilities**
**Problem**: QA template lists 5 quality dimensions but shows **no way to actually execute them**.

**Evidence** (from [qa.md.tmpl](gastown/internal/templates/roles/qa.md.tmpl)):
- Section "Quality Checklist" shows 5 dimensions: architecture compliance, code quality, test coverage, standards, security
- BUT: No documented method for QA to:
  - Know what architecture the code should comply with
  - Actually verify against that architecture
  - File defects and have them routed back to task owner
  - Report results back to Refinery

**Impact**: QA becomes a checklist reader, not an verifier. High-risk code could pass if QA doesn't feel like checking security.

**Recommendation**: Add explicit commands:
```bash
gt qa review <task-id>           # Opens task, shows architecture, code, tests
gt qa file-defect <task> <desc>  # Creates linked defect bead, notifies Polecat
gt qa approve <task>              # Signs off, creates "approved-by-qa" wisp
```

---

### 3. **Circular Dependency: Architect ↔ Planner**
**Problem**: Planner needs Architect's decisions before creating tasks, but both start simultaneously.

**Evidence**:
- [planner.md.tmpl](gastown/internal/templates/roles/planner.md.tmpl) line ~130: "gt nudge <rig>/architect 'component question...'" assumes Architect is running
- [architect.md.tmpl](gastown/internal/templates/roles/architect.md.tmpl) line ~50: "Wait for Planner to ask" – Architect is reactive
- [up.go](gastown/internal/cmd/up.go#L810-L820): Both started concurrently in worker pool

**Impact**: Race condition. Planner might start creating tasks before Architect has loaded the SPEC context, resulting in tasks that violate architecture.

**Recommendation**: Add startup dependency check in [up.go](gastown/internal/cmd/up.go):
```go
// Wait for Architect health before activating Planner
if !health.IsHealthy(constants.RoleArchitect, rigName) {
    log.Warnf("Delaying Planner tasks - Architect not healthy")
    // Queue nudges until ready
}
```

---

### 4. **Acceptance Criteria Not Enforced**
**Problem**: Planner's tasks include "Acceptance Criteria" format, but QA template doesn't verify them.

**Evidence**:
- [planner.md.tmpl](gastown/internal/templates/roles/planner.md.tmpl) shows task format with "acceptance criteria" field
- [qa.md.tmpl](gastown/internal/templates/roles/qa.md.tmpl) quality checklist (section 3) checks "Are there tests?" but NOT "Do tests verify acceptance criteria?"
- No explicit section showing QA validating code against acceptance criteria

**Impact**: Features pass QA/merge that don't solve the business problem because acceptance criteria were never verified.

**Recommendation**: Add mandatory QA step:
```markdown
## Acceptance Criteria Verification
For each criterion in the task:
- [ ] Criterion 1: [described in task]
- [ ] Criterion 2: [described in task]
...
All must PASS or QA fails the task.
```

---

### 5. **No Defect-to-Rework Workflow**
**Problem**: QA can file defects with `bd create --type=bug`, but they're orphaned – not linked to original task.

**Evidence**:
- [qa.md.tmpl](gastown/internal/templates/roles/qa.md.tmpl) line ~210: "File defects when code doesn't meet standards"
- No workflow showing defect being routed back to Polecat
- No mechanism to re-task the work or increase task ID complexity

**Impact**: Defects pile up without being addressed; rework isn't tracked; task debt compounds.

**Recommendation**: QA defects should:
1. Include parent task ID: `bd create --type=bug --parent=<task-id>`
2. Trigger Polecat reassignment: send nudge to Polecat to rework
3. Track rejection count: if rejected 3x, escalate to Architect

---

### 6. **No Progress Visibility Across Roles**
**Problem**: Three roles report to different places (QA→Refinery, Planner→Mayor, Architect→mail) with fragmented status.

**Evidence**:
- QA updates Refinery inbox: `gt mail send <rig>/refinery`
- Planner updates Mayor inbox: `gt mail send mayor`
- Architect updates via mail: `gt mail send planner`
- Mayor has no unified view of where a SPEC is in the system

**Impact**: Mayor can't answer "Where is this feature in the pipeline?" Bottlenecks invisible until merge fails.

**Recommendation**: Create status bead type flowing through system:
```go
SPEC_RECEIVED → ARCH_REVIEW_ASSIGNED → ARCH_REVIEW_COMPLETE 
→ PLANNING_ASSIGNED → PLANNING_COMPLETE → TASKS_READY 
→ IMPL_IN_PROGRESS → QA_REVIEW_IN_PROGRESS → QA_APPROVED 
→ READY_FOR_MERGE
```

Mayor can query `bd show --type=status --parent=<spec-id>` to see progress.

---

## 🟡 Architectural Vulnerabilities

### 7. **Security Review Not Mandatory**
- QA checklist has security checkbox but it's discretionary
- **Recommendation**: Make security review mandatory for auth/DB/API code patterns
- Consider separate Security role or automatic escalation for sensitive paths

### 8. **No Code Coverage Enforcement**
- QA checks "Are there tests?" but no quantitative threshold
- Coverage could systematically degrade
- **Recommendation**: Integrate coverage metrics – QA automatically fails if coverage < 80% (configurable)

### 9. **Context Cycling for Long Reviews Missing**
- Health config specifies `stuck_threshold = "1h"` but QA might be mid-review at 55min
- **Recommendation**: Add "context handoff protocol" to QA/Architect/Planner templates – how to save context and resume

### 10. **Architecture Decisions Not Stored**
- Architect responds to questions via mail but ADRs aren't archived
- Future developers can't see WHY decisions were made
- **Recommendation**: Create ADR (Architecture Decision Record) bead type, store in `{rig}/decisions`

---

## 📋 Files Summary

**Core Implementation**:
- [qa.go](gastown/internal/cmd/qa.go) (154 lines) - lifecycle commands
- [planner.go](gastown/internal/cmd/planner.go) (106 lines) - lifecycle commands
- [architect.go](gastown/internal/cmd/architect.go) (154 lines) - lifecycle commands

**Role Context & Instructions**:
- [qa.md.tmpl](gastown/internal/templates/roles/qa.md.tmpl) (200+ lines)
- [planner.md.tmpl](gastown/internal/templates/roles/planner.md.tmpl) (200+ lines)
- [architect.md.tmpl](gastown/internal/templates/roles/architect.md.tmpl) (200+ lines)

**Configuration**:
- [qa.toml](gastown/internal/config/roles/qa.toml) - TOML config with health checks
- [planner.toml](gastown/internal/config/roles/planner.toml) - town-level scope
- [architect.toml](gastown/internal/config/roles/architect.toml) - per-rig scope

**Integration**:
- [session/names.go](gastown/internal/session/names.go#L40-L60) - session naming functions
- [up.go](gastown/internal/cmd/up.go#L333-L340) - Planner startup
- [up.go](gastown/internal/cmd/up.go#L810-L820) - Architect/QA startup

---

## 🎯 Top 3 Fixes (Priority Order)

1. **Define explicit QA workflow** (2-3 days)
   - Create `gt qa review <task>` command
   - Implement defect filing with parent task linking
   - Mandate acceptance criteria verification step

2. **Fix Architect-Planner ordering** (1 day)
   - Add health check in startup to ensure Architect ready before Planner tasks activate
   - Prevent race condition in task creation

3. **Implement traceability** (3-4 days)
   - Add `spec-id` field to all tasks
   - Create status bead type for progress tracking
   - Store ADRs in beads for future reference

---

**Session notes saved to session memory for reference.**
