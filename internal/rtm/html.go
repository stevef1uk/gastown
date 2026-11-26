package rtm

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// HTML generates a standalone, interactive HTML report with search, category filtering,
// and scrollable tables to display full un-truncated paths and descriptions.
func (r *RTMReport) HTML() string {
	var sb strings.Builder

	title := fmt.Sprintf("Requirements Traceability Matrix — %s", html.EscapeString(r.Rig))
	activePhase := html.EscapeString(r.ActivePhase)
	if activePhase == "" {
		activePhase = "Complete / N/A"
	}

	coverageColor := "#10b981" // green
	if r.Failures > 0 {
		coverageColor = "#ef4444" // red
	} else if r.Warnings > 0 {
		coverageColor = "#f59e0b" // amber
	}

	sb.WriteString(fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s</title>
<style>
:root {
  --bg: #0b0f19;
  --card-bg: #111827;
  --card-border: #1f2937;
  --text: #f3f4f6;
  --text-muted: #9ca3af;
  --accent: #38bdf8;
  --accent-dim: rgba(56, 189, 248, 0.15);
  --success: #10b981;
  --success-dim: rgba(16, 185, 129, 0.15);
  --warning: #f59e0b;
  --warning-dim: rgba(245, 158, 11, 0.15);
  --danger: #ef4444;
  --danger-dim: rgba(239, 68, 68, 0.15);
  --mono: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  background-color: var(--bg);
  color: var(--text);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  padding: 2rem;
  line-height: 1.5;
}
.container { max-width: 1400px; margin: 0 auto; }
header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 2rem;
  flex-wrap: wrap;
  gap: 1rem;
}
h1 { font-size: 1.85rem; font-weight: 700; color: #fff; }
.meta-badges { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: 0.5rem; }
.badge {
  display: inline-flex;
  align-items: center;
  padding: 0.25rem 0.65rem;
  font-size: 0.8rem;
  font-weight: 600;
  border-radius: 9999px;
  background: var(--card-border);
  color: var(--text);
}
.badge-accent { background: var(--accent-dim); color: var(--accent); border: 1px solid rgba(56, 189, 248, 0.3); }
.badge-success { background: var(--success-dim); color: var(--success); border: 1px solid rgba(16, 185, 129, 0.3); }
.badge-warning { background: var(--warning-dim); color: var(--warning); border: 1px solid rgba(245, 158, 11, 0.3); }
.badge-danger { background: var(--danger-dim); color: var(--danger); border: 1px solid rgba(239, 68, 68, 0.3); }

/* KPI Cards */
.kpi-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 1rem;
  margin-bottom: 2rem;
}
.kpi-card {
  background: var(--card-bg);
  border: 1px solid var(--card-border);
  border-radius: 0.75rem;
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
}
.kpi-title { font-size: 0.825rem; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600; }
.kpi-value { font-size: 1.75rem; font-weight: 700; color: #fff; margin-top: 0.25rem; }
.kpi-sub { font-size: 0.8rem; color: var(--text-muted); margin-top: 0.25rem; }

/* Sections */
.section {
  background: var(--card-bg);
  border: 1px solid var(--card-border);
  border-radius: 0.75rem;
  margin-bottom: 2rem;
  overflow: hidden;
}
.section-header {
  padding: 1.25rem 1.5rem;
  border-bottom: 1px solid var(--card-border);
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 1rem;
}
.section-title { font-size: 1.15rem; font-weight: 600; color: #fff; }

/* Filter Controls */
.controls-bar {
  display: flex;
  align-items: center;
  gap: 1rem;
  flex-wrap: wrap;
}
.search-input {
  background: #0f172a;
  border: 1px solid var(--card-border);
  color: var(--text);
  padding: 0.45rem 0.85rem;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  min-width: 260px;
}
.search-input:focus { outline: none; border-color: var(--accent); }
.pills { display: flex; gap: 0.4rem; flex-wrap: wrap; }
.pill-btn {
  background: transparent;
  border: 1px solid var(--card-border);
  color: var(--text-muted);
  padding: 0.3rem 0.75rem;
  border-radius: 0.375rem;
  font-size: 0.8rem;
  cursor: pointer;
  transition: all 0.15s ease;
}
.pill-btn:hover { color: #fff; border-color: var(--text-muted); }
.pill-btn.active {
  background: var(--accent-dim);
  color: var(--accent);
  border-color: var(--accent);
  font-weight: 600;
}

/* Scrollable Tables */
.table-wrapper {
  overflow-x: auto;
  width: 100%%;
  -webkit-overflow-scrolling: touch;
}
table {
  width: 100%%;
  border-collapse: collapse;
  text-align: left;
  font-size: 0.875rem;
}
th {
  background: #0f172a;
  color: var(--text-muted);
  font-weight: 600;
  padding: 0.85rem 1.25rem;
  border-bottom: 1px solid var(--card-border);
  white-space: nowrap;
}
td {
  padding: 0.85rem 1.25rem;
  border-bottom: 1px solid var(--card-border);
  vertical-align: top;
}
tr:hover td { background: rgba(255, 255, 255, 0.02); }
.mono { font-family: var(--mono); font-size: 0.825rem; }
.path-cell {
  font-family: var(--mono);
  font-size: 0.825rem;
  color: #38bdf8;
  word-break: break-all;
  max-width: 380px;
}
.bead-id {
  font-family: var(--mono);
  font-weight: 600;
  color: #a78bfa;
  white-space: nowrap;
}
.audit-pass { color: var(--success); font-weight: 600; display: inline-flex; align-items: center; gap: 0.25rem; }
.audit-warn { color: var(--warning); font-weight: 600; display: inline-flex; align-items: center; gap: 0.25rem; }
.audit-fail { color: var(--danger); font-weight: 600; display: inline-flex; align-items: center; gap: 0.25rem; }

/* Expandable scenarios */
details summary {
  cursor: pointer;
  color: var(--text-muted);
  font-size: 0.8rem;
  margin-top: 0.35rem;
}
details summary:hover { color: var(--accent); }
.details-content {
  margin-top: 0.5rem;
  padding: 0.5rem 0.75rem;
  background: #0f172a;
  border-radius: 0.375rem;
  font-size: 0.8rem;
}
.details-content ul { padding-left: 1.25rem; margin-top: 0.25rem; }
.details-content li { margin-bottom: 0.2rem; }

footer {
  text-align: center;
  color: var(--text-muted);
  font-size: 0.8rem;
  margin-top: 2rem;
  padding-bottom: 1rem;
}
</style>
</head>
<body>
<div class="container">

<header>
  <div>
    <h1>Requirements Traceability Matrix</h1>
    <div class="meta-badges">
      <span class="badge badge-accent">Rig: %s</span>
      <span class="badge">Phase: %s</span>
      <span class="badge">Generated: %s</span>
    </div>
  </div>
  <div>
    <div class="badge badge-success" style="font-size: 1rem; padding: 0.5rem 1rem; background: %s15; color: %s; border-color: %s40;">
      ✓ %.1f%%%% Coverage (%d/%d Verified)
    </div>
  </div>
</header>

<div class="kpi-grid">
  <div class="kpi-card">
    <div class="kpi-title">Requirements</div>
    <div class="kpi-value">%d</div>
    <div class="kpi-sub">%d passed, %d warnings, %d failed</div>
  </div>
  <div class="kpi-card">
    <div class="kpi-title">Total Tracked Beads</div>
    <div class="kpi-value">%d</div>
    <div class="kpi-sub">Across all delivery categories</div>
  </div>
  <div class="kpi-card">
    <div class="kpi-title">App Components</div>
    <div class="kpi-value">%d</div>
    <div class="kpi-sub">Web, Data, UI & Core modules</div>
  </div>
  <div class="kpi-card">
    <div class="kpi-title">Infrastructure & DevOps</div>
    <div class="kpi-value">%d</div>
    <div class="kpi-sub">K8s & Docker container manifests</div>
  </div>
  <div class="kpi-card">
    <div class="kpi-title">Build & Config</div>
    <div class="kpi-value">%d</div>
    <div class="kpi-sub">Package managers, configs & docs</div>
  </div>
</div>
`,
		title,
		html.EscapeString(r.Rig),
		activePhase,
		r.GeneratedAt.Format(time.RFC3339),
		coverageColor, coverageColor, coverageColor,
		r.ScorePct, r.Passed, r.TotalReqs,
		r.TotalReqs, r.Passed, r.Warnings, r.Failures,
		r.Summary.TotalBeads,
		r.Summary.Components,
		r.Summary.Infrastructure,
		r.Summary.Build+r.Summary.DocsConfig,
	))

	// Table 1: Requirements Verification Matrix
	sb.WriteString(`
<div class="section">
  <div class="section-header">
    <div class="section-title">Requirements & Quality Verification Gates</div>
  </div>
  <div class="table-wrapper">
    <table>
      <thead>
        <tr>
          <th>Requirement</th>
          <th>Bead</th>
          <th>Phase</th>
          <th>Verification Test File</th>
          <th>Level</th>
          <th>Audit</th>
        </tr>
      </thead>
      <tbody>
`)

	for _, row := range r.Rows {
		cleanTitle := CleanRequirementTitle(row.ReqID, row.Requirement)
		auditHTML := `<span class="audit-pass">✓ PASS</span>`
		if row.AuditStatus == AuditWarn {
			auditHTML = `<span class="audit-warn">⚠ WARN</span>`
		} else if row.AuditStatus == AuditFail {
			auditHTML = `<span class="audit-fail">✗ FAIL</span>`
		} else if row.AuditStatus == AuditPending {
			auditHTML = `<span style="color:var(--text-muted)">⏳ PEND</span>`
		}

		detailsHTML := ""
		if len(row.Scenarios) > 0 || len(row.Assertions) > 0 {
			var d strings.Builder
			d.WriteString("<details><summary>Scenarios & Assertions</summary><div class=\"details-content\">")
			if len(row.Scenarios) > 0 {
				d.WriteString("<strong>Scenarios:</strong><ul>")
				for _, sc := range row.Scenarios {
					d.WriteString(fmt.Sprintf("<li>%s</li>", html.EscapeString(sc)))
				}
				d.WriteString("</ul>")
			}
			if len(row.Assertions) > 0 {
				d.WriteString("<strong>Assertions:</strong><ul>")
				for _, as := range row.Assertions {
					d.WriteString(fmt.Sprintf("<li>%s</li>", html.EscapeString(as)))
				}
				d.WriteString("</ul>")
			}
			d.WriteString("</div></details>")
			detailsHTML = d.String()
		}

		sb.WriteString(fmt.Sprintf(`
        <tr>
          <td>
            <div style="font-weight: 600; color: #fff;">%s</div>
            <div style="color: var(--text-muted); font-size: 0.8rem; margin-top: 0.2rem;">%s</div>
            %s
          </td>
          <td><span class="bead-id">%s</span></td>
          <td><span class="badge">%s</span></td>
          <td class="path-cell">%s</td>
          <td><span class="badge">%s</span></td>
          <td>%s</td>
        </tr>`,
			html.EscapeString(cleanTitle),
			html.EscapeString(row.Requirement),
			detailsHTML,
			html.EscapeString(row.BeadID),
			html.EscapeString(row.Phase),
			html.EscapeString(CleanArtifactPath(row.TestFile, r.Rig)),
			html.EscapeString(row.Level),
			auditHTML,
		))
	}

	sb.WriteString(`
      </tbody>
    </table>
  </div>
</div>
`)

	// Table 2: Implementation & Component Alignment
	sb.WriteString(fmt.Sprintf(`
<div class="section">
  <div class="section-header">
    <div class="section-title">Implementation & Component Alignment (%d Total Tracked Beads)</div>
    <div class="controls-bar">
      <input type="text" id="beadSearch" class="search-input" placeholder="Search beads, artifacts, purposes..." oninput="filterBeads()">
      <div class="pills">
        <button class="pill-btn active" onclick="setCategoryFilter('all', this)">All (%d)</button>
        <button class="pill-btn" onclick="setCategoryFilter('Requirement', this)">Requirements (%d)</button>
        <button class="pill-btn" onclick="setCategoryFilter('Component', this)">Components (%d)</button>
        <button class="pill-btn" onclick="setCategoryFilter('Infrastructure', this)">Infrastructure (%d)</button>
        <button class="pill-btn" onclick="setCategoryFilter('Build & Tooling', this)">Build (%d)</button>
        <button class="pill-btn" onclick="setCategoryFilter('Testing', this)">Testing (%d)</button>
        <button class="pill-btn" onclick="setCategoryFilter('Docs & Config', this)">Docs (%d)</button>
      </div>
    </div>
  </div>
  <div class="table-wrapper">
    <table id="beadsTable">
      <thead>
        <tr>
          <th>Bead</th>
          <th>Status</th>
          <th>Category</th>
          <th>Target Artifact</th>
          <th>Capability / Purpose</th>
        </tr>
      </thead>
      <tbody>
`,
		len(r.Beads),
		len(r.Beads),
		r.Summary.Requirements,
		r.Summary.Components,
		r.Summary.Infrastructure,
		r.Summary.Build,
		r.Summary.Testing,
		r.Summary.DocsConfig,
	))

	for _, b := range r.Beads {
		statusClass := "badge"
		if b.Status == "closed" {
			statusClass = "badge badge-success"
		} else if b.Status == "in_progress" || b.Status == "open" {
			statusClass = "badge badge-warning"
		}

		catClass := "badge"
		if b.Category == CategoryRequirement {
			catClass = "badge badge-accent"
		} else if strings.HasPrefix(b.Category, "Component") {
			catClass = "badge"
		} else if b.Category == CategoryInfrastructure {
			catClass = "badge badge-warning"
		}

		artifact := b.Artifact
		if artifact == "" {
			artifact = b.Requirement
		}

		purpose := b.Purpose
		if purpose == "" {
			purpose = b.Description
			if purpose == "" {
				purpose = b.Title
			}
		}

		sb.WriteString(fmt.Sprintf(`
        <tr data-category="%s">
          <td><span class="bead-id">%s</span></td>
          <td><span class="%s">%s</span></td>
          <td><span class="%s">%s</span></td>
          <td class="path-cell">%s</td>
          <td><span style="color: #fff; font-weight: 500;">%s</span></td>
        </tr>`,
			html.EscapeString(b.Category),
			html.EscapeString(b.ID),
			statusClass,
			html.EscapeString(b.Status),
			catClass,
			html.EscapeString(b.Category),
			html.EscapeString(artifact),
			html.EscapeString(purpose),
		))
	}

	sb.WriteString(`
      </tbody>
    </table>
  </div>
</div>

<footer>
  Gas Town Autonomous Multi-Agent Delivery Platform • Requirements Traceability Matrix
</footer>

</div>

<script>
let currentCategory = 'all';

function setCategoryFilter(cat, btn) {
  currentCategory = cat;
  document.querySelectorAll('.pill-btn').forEach(b => b.classList.remove('active'));
  btn.classList.add('active');
  filterBeads();
}

function filterBeads() {
  const query = document.getElementById('beadSearch').value.toLowerCase().trim();
  const rows = document.querySelectorAll('#beadsTable tbody tr');

  rows.forEach(row => {
    const rowCat = row.getAttribute('data-category') || '';
    const text = row.innerText.toLowerCase();

    let catMatch = false;
    if (currentCategory === 'all') {
      catMatch = true;
    } else if (currentCategory === 'Component') {
      catMatch = rowCat.startsWith('Component');
    } else {
      catMatch = rowCat.includes(currentCategory);
    }

    const queryMatch = !query || text.includes(query);

    if (catMatch && queryMatch) {
      row.style.display = '';
    } else {
      row.style.display = 'none';
    }
  });
}
</script>
</body>
</html>
`)

	return sb.String()
}
