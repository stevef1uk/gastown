# Proposed Prompt Enhancements

Concise additions to address critical gaps in shortened prompts while maintaining small size.

## High-Priority Fixes

### 1. Beads Routing (Mayor + Polecat)
Add 1-line reminder:
```
Beads route by prefix: bd show gt-xyz → gastown, bd show hq-abc → town.
Routes in {{ .TownRoot }}/.beads/routes.jsonl. File issues where code lives.
```

### 2. Docker-Compose Template Approach (Mayor)
Add inline reminder - critical for E2E testing:
```
ALWAYS use multi-stage Dockerfile build: target: playwright in docker-compose templates.
Never use image: {{PLAYWRIGHT_IMAGE}} external image approach - it breaks tests.
The multi-stage approach pre-installs npm deps during build, supports Go/Python/Node generically,
and avoids runtime npm install failures (empty package.json, version mismatches).
Refer to: internal/orchestrator/town/templates/rig-init/docker-compose.*.yml
```

### 3. Go Version Consistency (Mayor)
Add inline reminder:
```
Verify go.mod go version matches SPEC.md specification. If go.mod has go 1.25 but SPEC.md says 1.22,
fix go.mod - do NOT change the Dockerfile base image version. The Dockerfile is templated and must
remain generic for all rig types.
```

### 2. Directory Discipline Consequence (Polecat)
Add warning inline:
```
WORK IN: {{ .RigName }}/polecats/{{ .Polecat }}/ — NEVER edit in rig root or work is LOST (no .git).
```

### 3. Dolt Persistence (All roles)
Add single line:
```
Beads auto-save to Dolt. No manual sync needed. If bd hangs >5s, escalate: gt escalate "Dolt hung".
```

### 4. Escalation Options (Polecat)
Replace single method with:
```
Escalate if stuck >15min: gt escalate "desc" -s HIGH (preferred), or gt mail send {{ .RigName }}/witness -s "HELP: ..." --stdin.
```

### 5. Communication Hygiene (Polecat)
Add compact rule:
```
Use nudge (free) for routine. Mail creates Dolt commit (0-1 per session). gt done handles completion notification — don't mail "done".
```

## Medium-Priority (Optional)

### 6. Memory Types
Add inline:
```
gt remember "insight" — types: feedback (corrections), project (decisions), user (preferences), reference (links).
```

### 7. GitHub Remote Verification
Add to completion section:
```
Before push: git remote -v to verify URL. Never assume org.
```

## Implementation

These 9 additions total ~400 words. Add as inline reminders in respective role templates, not new sections.