package rtm

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"
)

// HTML generates a standalone, interactive HTML report with search, category filtering,
// scrollable tables, and an interactive code viewing panel to inspect actual rig artifacts on disk.
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

	fileDataJSON, err := json.Marshal(r.FileContents)
	if err != nil {
		fileDataJSON = []byte("{}")
	}
	filePathsJSON, err := json.Marshal(r.FileAbsolutePaths)
	if err != nil {
		filePathsJSON = []byte("{}")
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
  text-decoration: none;
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
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
  word-break: break-all;
  max-width: 420px;
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

/* Interactive File Links */
.file-link {
  color: #38bdf8;
  text-decoration: none;
  font-family: var(--mono);
  font-size: 0.825rem;
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  cursor: pointer;
  border-bottom: 1px dashed rgba(56, 189, 248, 0.4);
  padding: 0.1rem 0.25rem;
  border-radius: 0.25rem;
  transition: all 0.15s ease;
}
.file-link:hover {
  color: #7dd3fc;
  border-bottom-color: #38bdf8;
  background: var(--accent-dim);
}
.file-icon { font-size: 0.85rem; opacity: 0.8; }

/* Viewing Panel Below */
.viewer-section {
  border: 1px solid #334155;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.5);
  margin-top: 2.5rem;
}
.viewer-header {
  background: #0f172a;
}
.viewer-meta {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
}
.viewer-filename {
  font-family: var(--mono);
  font-size: 1.05rem;
  font-weight: 700;
  color: #fff;
}
.viewer-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.viewer-empty {
  padding: 3.5rem 2rem;
  text-align: center;
  color: var(--text-muted);
  font-size: 0.95rem;
}
.code-container {
  display: flex;
  background: #080d1a;
  max-height: 560px;
  overflow: auto;
}
.line-numbers {
  padding: 1rem 0.75rem;
  text-align: right;
  color: #4b5563;
  font-family: var(--mono);
  font-size: 0.8rem;
  line-height: 1.5;
  user-select: none;
  background: #060913;
  border-right: 1px solid var(--card-border);
}
pre {
  margin: 0;
  padding: 1rem;
  flex: 1;
}
.code-body {
  font-family: var(--mono);
  font-size: 0.825rem;
  line-height: 1.5;
  color: #e2e8f0;
  white-space: pre;
}

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
    <div style="font-size: 0.8rem; color: var(--text-muted);">Click any test file to view assertions below</div>
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

		cleanTestPath := CleanArtifactPath(row.TestFile, r.Rig)
		fileLinkHTML := fmt.Sprintf(`<a href="javascript:void(0)" class="file-link" onclick="openFileViewer('%s')"><span class="file-icon">📄</span>%s</a>`,
			html.EscapeString(cleanTestPath), html.EscapeString(cleanTestPath))

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
			fileLinkHTML,
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
          <th>Target Artifact (Click to View)</th>
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

		fileLinkHTML := fmt.Sprintf(`<a href="javascript:void(0)" class="file-link" onclick="openFileViewer('%s')"><span class="file-icon">📄</span>%s</a>`,
			html.EscapeString(artifact), html.EscapeString(artifact))

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
			fileLinkHTML,
			html.EscapeString(purpose),
		))
	}

	sb.WriteString(`
      </tbody>
    </table>
  </div>
</div>
`)

	// Section 3: Interactive Artifact Code Viewer Panel
	sb.WriteString(`
<div id="fileViewerSection" class="section viewer-section">
  <div class="section-header viewer-header">
    <div class="viewer-meta">
      <span class="file-icon" style="font-size: 1.25rem;">📄</span>
      <span id="viewerFileName" class="viewer-filename">Select a file above to view contents</span>
      <span id="viewerBadge" class="badge" style="display:none;"></span>
      <span id="viewerLines" class="badge badge-accent" style="display:none;"></span>
    </div>
    <div class="viewer-actions">
      <a id="viewerRawLink" href="#" class="pill-btn" target="_blank" style="display:none;">Open Raw File ↗</a>
      <button id="copyCodeBtn" class="pill-btn" onclick="copyViewerCode()" style="display:none;">Copy Code</button>
      <button id="closeViewerBtn" class="pill-btn" onclick="closeViewer()" style="display:none;">✕ Close</button>
    </div>
  </div>
  <div id="viewerEmptyState" class="viewer-empty">
    <div style="font-size: 1.75rem; margin-bottom: 0.5rem; opacity: 0.6;">🔍</div>
    <div>Click any test file or target artifact above to inspect its implementation on disk.</div>
  </div>
  <div id="viewerContent" class="code-container" style="display: none;">
    <div id="lineNumbers" class="line-numbers"></div>
    <pre><code id="viewerCode" class="code-body"></code></pre>
  </div>
</div>

<footer>
  Gas Town Autonomous Multi-Agent Delivery Platform • Requirements Traceability Matrix
</footer>

</div>

<script>
const fileData = ` + string(fileDataJSON) + `;
const filePaths = ` + string(filePathsJSON) + `;

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

function openFileViewer(path) {
  const content = fileData[path] || fileData[path.replace(/^\/+/, '')] || '';
  const absPath = filePaths[path] || filePaths[path.replace(/^\/+/, '')] || '';

  const viewerSection = document.getElementById('fileViewerSection');
  const fileNameEl = document.getElementById('viewerFileName');
  const badgeEl = document.getElementById('viewerBadge');
  const linesEl = document.getElementById('viewerLines');
  const rawLinkEl = document.getElementById('viewerRawLink');
  const copyBtn = document.getElementById('copyCodeBtn');
  const closeBtn = document.getElementById('closeViewerBtn');
  const emptyState = document.getElementById('viewerEmptyState');
  const contentEl = document.getElementById('viewerContent');
  const codeEl = document.getElementById('viewerCode');
  const lineNumbersEl = document.getElementById('lineNumbers');

  fileNameEl.textContent = path;

  if (!content) {
    emptyState.style.display = 'block';
    emptyState.innerHTML = '<div style="font-size:1.5rem; margin-bottom:0.5rem; color:var(--warning);">⚠️</div><div>Artifact file not found on disk: <code>' + path + '</code></div>';
    contentEl.style.display = 'none';
    badgeEl.style.display = 'none';
    linesEl.style.display = 'none';
    rawLinkEl.style.display = 'none';
    copyBtn.style.display = 'none';
    closeBtn.style.display = 'inline-flex';
    viewerSection.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    return;
  }

  emptyState.style.display = 'none';
  contentEl.style.display = 'flex';
  badgeEl.style.display = 'inline-flex';
  linesEl.style.display = 'inline-flex';
  copyBtn.style.display = 'inline-flex';
  closeBtn.style.display = 'inline-flex';

  // Format badge
  const ext = path.split('.').pop().toLowerCase();
  badgeEl.textContent = ext.toUpperCase() + ' File';

  // Code body
  codeEl.textContent = content;

  // Line numbers
  const lines = content.split('\n');
  let nums = '';
  for (let i = 1; i <= lines.length; i++) {
    nums += i + '\n';
  }
  lineNumbersEl.textContent = nums;

  if (absPath) {
    rawLinkEl.style.display = 'inline-flex';
    rawLinkEl.href = 'file://' + absPath;
  } else {
    rawLinkEl.style.display = 'none';
  }

  viewerSection.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function closeViewer() {
  document.getElementById('viewerContent').style.display = 'none';
  document.getElementById('viewerEmptyState').style.display = 'block';
  document.getElementById('viewerEmptyState').innerHTML = '<div style="font-size: 1.75rem; margin-bottom: 0.5rem; opacity: 0.6;">🔍</div><div>Click any test file or target artifact above to inspect its implementation on disk.</div>';
  document.getElementById('viewerFileName').textContent = 'Select a file above to view contents';
  document.getElementById('viewerBadge').style.display = 'none';
  document.getElementById('viewerLines').style.display = 'none';
  document.getElementById('viewerRawLink').style.display = 'none';
  document.getElementById('copyCodeBtn').style.display = 'none';
  document.getElementById('closeViewerBtn').style.display = 'none';
}

function copyViewerCode() {
  const code = document.getElementById('viewerCode').textContent;
  navigator.clipboard.writeText(code).then(() => {
    const btn = document.getElementById('copyCodeBtn');
    const old = btn.textContent;
    btn.textContent = '✓ Copied!';
    setTimeout(() => { btn.textContent = old; }, 1500);
  });
}
</script>
</body>
</html>
`)

	return sb.String()
}
