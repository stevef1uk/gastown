package orchestrator

// RigFlowStaticURLContractGuidance is injected into rig-flow prompts (GT-VERIFY-008).
// Single source of truth: architecture HTTP table + index.html refs; smoke curls follow those.
const RigFlowStaticURLContractGuidance = "Static asset URLs come **only** from **architecture.md** (HTTP routes) and the exact " +
	"`src`/`href` paths in `web/index.html`. Implementation, unit tests, and QA smoke must use the **same** URLs. " +
	"gt-agent builds runtime smoke curls from architecture + index.html. **Do not** change HTML or handlers to satisfy a failing curl unless architecture defines that URL."

// RigFlowStaticURLContractShort is a one-line form for tables and implementation rows.
const RigFlowStaticURLContractShort = "URLs from **architecture.md** + `web/index.html` only (gt-agent smoke uses those paths — do not guess `/app.js` vs `/static/…`)."
