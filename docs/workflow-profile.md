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
