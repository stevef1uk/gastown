# Workflow Profile System

## Authority Model

**SPEC layout tree is the single authoritative source** for required files when it exists.
- Prose backtick refs in SPEC are documentation only (often mention files negatively: "no package.json")
- `parseSpecLayoutTree` extracts the complete required set from the tree
- Architecture.md adds/overrides on top

## Merge Logic (arch wins on conflict)

```
authoritative = merge(SPEC_tree_paths, architecture_paths)
```
- Same basename at different paths → architecture.md wins
- Architecture can add extra files not in SPEC tree
- Architecture cannot remove SPEC tree files

## Sync Timing

| Event | Action |
|-------|--------|
| `spec_review` success | `gt rig spec-index` (LLM hallucinated profile) |
| `design` success | **Sync runs**: re-derives from SPEC + architecture.md → writes clean profile |
| `design_review` | QA validates clean profile |

## QA Validation (design_review)

`ValidateRigWorkflowProfileForQA` catches before planning:
- Layout drift: same basename at different paths across phases
- Empty `required_files` in any delivery phase
- Hallucinated entries (wildcards, route stubs, URLs)
- Verify commands referencing files not in `required_files`
- Explicit numbered SPEC phases omitted by indexing or architecture sync
- Declared implementation files missing from the profile, including supplemental layout sections
- Buildable Node projects without a manifest or phases delivering their source without a production-build gate

Standalone `# Phase N — Title` headings are declarations even without a wrapping
delivery-phase section. Failed file assignment must report an error rather than
replace these phases with directory-based grouping. Identical verification commands
alone do not justify merging explicit SPEC phases. SPEC scope takes precedence over
an earlier generated profile; architecture rework must correct its canonical layout,
delivery table, and requirements consistently.

### Explicit delivery contracts

An architecture can place one fenced YAML `delivery_phases:` block under its
single `## Delivery phases` section, using the existing phase fields (`id`,
`title`, `spec_focus`, `required_files`, `qa_verify_command`, `depends_on`). Sync
validates this contract before saving it and sets `explicit_delivery_contract`.
Such profiles retain declared ownership, dependencies and commands on load/save;
heuristic splitting, Docker relocation and command replacement apply only to
inferred profiles. Every declared file must have a phase owner. Changed phase
files/commands invalidate that phase's old completion, and design rework clears
test-plan approval/freeze. Split phases retain their original SPEC title in
`spec_focus`.

Duplicate canonical layout, delivery or requirements sections are rejected;
amend the existing section instead of appending another authoritative copy.
Design and design-review retries pause after three failures against an unchanged
effective profile (`no_progress_limit` / `progress_files` hooks). Correct the
contract and resume, or explicitly reset the workflow to clear its retry budget.

Document judges resolve `GT_JUDGE_ENDPOINT` / `GT_JUDGE_MODEL`, then general LLM
environment settings and configured judge/town QA models. Invalid API responses
are validation errors, rather than an implicit successful Docker review.

## Phase Population

`rebuildDeliveryPhasesFromAuthoritative`:
1. Filters each phase's existing files to authoritative set
2. Places any unplaced authoritative files into best-matching phase (by path prefix)
3. Result: every phase has verifiable files, no phase is empty

## Architect Guidelines

**Can add files:**
- New implementation files with correct `layout_root/` prefix
- Test files matching SPEC requirements
- Deployment/scripts not in SPEC tree

**Cannot:**
- Remove SPEC tree files
- Change layout_root prefix
- Add files with conflicting basenames (different paths)

**Must include in architecture.md:**
- All SPEC tree files with `layout_root/` prefix
- Any additional files
- Backtick-quoted paths in tables/lists
