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

/* Document Cards Grid */
.docs-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 1rem;
  padding: 1.25rem;
}
.doc-card {
  background: #0d1527;
  border: 1px solid var(--card-border);
  border-radius: 0.65rem;
  padding: 1.15rem;
  display: flex;
  flex-direction: column;
  cursor: pointer;
  transition: all 0.2s ease;
  position: relative;
  text-decoration: none;
}
.doc-card:hover {
  border-color: var(--accent);
  transform: translateY(-2px);
  box-shadow: 0 4px 20px rgba(56, 189, 248, 0.15);
  background: #111d35;
}
.doc-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.75rem;
}
.doc-icon {
  font-size: 1.65rem;
  line-height: 1;
}
.doc-size-badge {
  font-family: var(--mono);
  font-size: 0.75rem;
  color: var(--text-muted);
  background: rgba(255, 255, 255, 0.05);
  padding: 0.2rem 0.5rem;
  border-radius: 9999px;
  border: 1px solid var(--card-border);
}
.doc-filename {
  font-family: var(--mono);
  font-size: 0.85rem;
  font-weight: 700;
  color: var(--accent);
  margin-bottom: 0.25rem;
}
.doc-heading {
  font-size: 0.95rem;
  font-weight: 600;
  color: #fff;
  margin-bottom: 0.35rem;
}
.doc-desc {
  font-size: 0.8rem;
  color: var(--text-muted);
  line-height: 1.4;
  flex: 1;
  margin-bottom: 0.75rem;
}
.doc-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  padding-top: 0.5rem;
  border-top: 1px solid rgba(255, 255, 255, 0.05);
}
.doc-read-cta {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--accent);
}
.doc-card:hover .doc-read-cta {
  text-decoration: underline;
}

/* Viewer Tabs */
.viewer-tabs {
  display: flex;
  gap: 0.35rem;
}
.viewer-tab {
  background: transparent;
  border: 1px solid var(--card-border);
  color: var(--text-muted);
  padding: 0.35rem 0.75rem;
  border-radius: 0.375rem;
  font-size: 0.8rem;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
}
.viewer-tab:hover {
  color: #fff;
  border-color: var(--text-muted);
}
.viewer-tab.active {
  background: var(--accent-dim);
  color: var(--accent);
  border-color: var(--accent);
}

/* Markdown Formatted Body Styling */
.markdown-container {
  padding: 2rem;
  max-height: 650px;
  overflow-y: auto;
  background: #080d1a;
  color: #e2e8f0;
  line-height: 1.65;
  font-size: 0.925rem;
}
.markdown-body h1, .markdown-body h2, .markdown-body h3, .markdown-body h4, .markdown-body h5, .markdown-body h6 {
  color: #fff;
  font-weight: 700;
  margin-top: 1.5rem;
  margin-bottom: 0.75rem;
  line-height: 1.3;
}
.markdown-body h1 { font-size: 1.65rem; border-bottom: 1px solid var(--card-border); padding-bottom: 0.5rem; }
.markdown-body h2 { font-size: 1.35rem; border-bottom: 1px solid rgba(255, 255, 255, 0.08); padding-bottom: 0.4rem; }
.markdown-body h3 { font-size: 1.15rem; color: #7dd3fc; }
.markdown-body h4 { font-size: 1.025rem; color: #a78bfa; }
.markdown-body p { margin-bottom: 1rem; }
.markdown-body ul, .markdown-body ol { margin-bottom: 1rem; padding-left: 1.5rem; }
.markdown-body li { margin-bottom: 0.35rem; }
.markdown-body hr { border: 0; height: 1px; background: var(--card-border); margin: 1.75rem 0; }
.markdown-body blockquote {
  border-left: 4px solid var(--accent);
  padding: 0.6rem 1rem;
  margin: 1rem 0;
  background: rgba(56, 189, 248, 0.06);
  border-radius: 0 0.375rem 0.375rem 0;
  color: #cbd5e1;
}
.markdown-body blockquote.alert-note {
  border-left-color: #38bdf8;
  background: rgba(56, 189, 248, 0.08);
}
.markdown-body blockquote.alert-tip {
  border-left-color: #10b981;
  background: rgba(16, 185, 129, 0.08);
}
.markdown-body blockquote.alert-important {
  border-left-color: #a78bfa;
  background: rgba(167, 139, 250, 0.08);
}
.markdown-body blockquote.alert-warning {
  border-left-color: #f59e0b;
  background: rgba(245, 158, 11, 0.08);
}
.markdown-body blockquote.alert-caution {
  border-left-color: #ef4444;
  background: rgba(239, 68, 68, 0.08);
}
.markdown-body code {
  font-family: var(--mono);
  background: #1e293b;
  color: #f1f5f9;
  padding: 0.15rem 0.4rem;
  border-radius: 0.25rem;
  font-size: 0.85em;
  border: 1px solid rgba(255, 255, 255, 0.08);
}
.code-fence-wrapper {
  margin: 1.25rem 0;
  border: 1px solid #1e293b;
  border-radius: 0.5rem;
  overflow: hidden;
  background: #050814;
}
.code-fence-header {
  background: #0f172a;
  padding: 0.35rem 0.85rem;
  font-family: var(--mono);
  font-size: 0.75rem;
  color: var(--text-muted);
  border-bottom: 1px solid #1e293b;
  display: flex;
  justify-content: space-between;
}
.code-fence-wrapper pre {
  margin: 0;
  border: none;
  border-radius: 0;
  padding: 0.85rem 1rem;
}
.markdown-body table {
  width: 100%%;
  border-collapse: collapse;
  margin: 1.25rem 0;
  font-size: 0.85rem;
}
.markdown-body th {
  background: #0f172a;
  color: #f8fafc;
  font-weight: 600;
  padding: 0.65rem 0.85rem;
  border: 1px solid var(--card-border);
  text-align: left;
}
.markdown-body td {
  padding: 0.65rem 0.85rem;
  border: 1px solid var(--card-border);
  vertical-align: top;
}
.markdown-body tr:nth-child(even) td {
  background: rgba(255, 255, 255, 0.02);
}
.markdown-body tr:hover td {
  background: rgba(255, 255, 255, 0.04);
}
.markdown-body strong { color: #fff; font-weight: 600; }
.markdown-body a { color: var(--accent); text-decoration: underline; }
.markdown-body input[type="checkbox"] { margin-right: 0.4rem; vertical-align: middle; }

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

	// Section: Workflow Specifications & Core Artifacts
	if len(r.WorkflowDocs) > 0 {
		sb.WriteString(`
<div class="section" style="margin-bottom: 2rem;">
  <div class="section-header">
    <div style="display: flex; align-items: center; gap: 0.5rem;">
      <span style="font-size: 1.25rem;">📚</span>
      <div class="section-title">Workflow Specifications & Core Artifacts</div>
    </div>
    <div style="font-size: 0.8rem; color: var(--text-muted);">
      Click any document to inspect formatted markdown specifications, architectures and plans below
    </div>
  </div>
  <div class="docs-grid">
`)
		for _, doc := range r.WorkflowDocs {
			sizeStr := formatFileSize(doc.Bytes)
			cleanDocPath := CleanArtifactPath(doc.Name, r.Rig)
			sb.WriteString(fmt.Sprintf(`
    <div class="doc-card" onclick="openFileViewer('%s')">
      <div class="doc-card-header">
        <div class="doc-icon">%s</div>
        <div class="doc-size-badge">%s</div>
      </div>
      <div class="doc-filename">%s</div>
      <div class="doc-heading">%s</div>
      <div class="doc-desc">%s</div>
      <div class="doc-footer">
        <span class="doc-read-cta">Read Document →</span>
      </div>
    </div>`,
				html.EscapeString(cleanDocPath),
				doc.Icon,
				html.EscapeString(sizeStr),
				html.EscapeString(cleanDocPath),
				html.EscapeString(doc.Title),
				html.EscapeString(doc.Description),
			))
		}
		sb.WriteString(`
  </div>
</div>
`)
	}

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

	// Section 3: Interactive Artifact Code & Document Viewer Panel
	sb.WriteString(`
<div id="fileViewerSection" class="section viewer-section">
  <div class="section-header viewer-header">
    <div class="viewer-meta">
      <span id="viewerFileIcon" class="file-icon" style="font-size: 1.25rem;">📄</span>
      <span id="viewerFileName" class="viewer-filename">Select a file above to view contents</span>
      <span id="viewerBadge" class="badge" style="display:none;"></span>
      <span id="viewerLines" class="badge badge-accent" style="display:none;"></span>

      <!-- Rendered vs Raw Tabs for Markdown / Specs -->
      <div id="viewerTabs" class="viewer-tabs" style="display:none; margin-left: 0.5rem;">
        <button id="tabRenderedBtn" class="viewer-tab active" onclick="switchViewerMode('rendered')">👁️ Formatted View</button>
        <button id="tabRawBtn" class="viewer-tab" onclick="switchViewerMode('raw')">📄 Raw Source</button>
      </div>
    </div>
    <div class="viewer-actions">
      <a id="viewerRawLink" href="#" class="pill-btn" target="_blank" style="display:none;">Open Raw File ↗</a>
      <button id="copyCodeBtn" class="pill-btn" onclick="copyViewerCode()" style="display:none;">Copy Content</button>
      <button id="closeViewerBtn" class="pill-btn" onclick="closeViewer()" style="display:none;">✕ Close</button>
    </div>
  </div>
  <div id="viewerEmptyState" class="viewer-empty">
    <div style="font-size: 1.75rem; margin-bottom: 0.5rem; opacity: 0.6;">🔍</div>
    <div>Click any specification document or verification test above to inspect its implementation on disk.</div>
  </div>
  <div id="viewerRendered" class="markdown-container markdown-body" style="display: none;"></div>
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
let currentViewerFile = '';
let currentViewerContent = '';
let currentViewerMode = 'rendered';

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

function switchViewerMode(mode) {
  currentViewerMode = mode;
  const isMd = currentViewerFile.toLowerCase().endsWith('.md');
  const renderedEl = document.getElementById('viewerRendered');
  const contentEl = document.getElementById('viewerContent');
  const tabRenderedBtn = document.getElementById('tabRenderedBtn');
  const tabRawBtn = document.getElementById('tabRawBtn');

  if (mode === 'rendered' && isMd) {
    if (tabRenderedBtn) tabRenderedBtn.classList.add('active');
    if (tabRawBtn) tabRawBtn.classList.remove('active');
    renderedEl.innerHTML = renderMarkdown(currentViewerContent);
    renderedEl.style.display = 'block';
    contentEl.style.display = 'none';
  } else {
    if (tabRenderedBtn) tabRenderedBtn.classList.remove('active');
    if (tabRawBtn) tabRawBtn.classList.add('active');
    renderedEl.style.display = 'none';
    contentEl.style.display = 'flex';
  }
}

function renderMarkdown(md) {
  if (!md) return '';

  // 1. Extract fenced code blocks
  const codeBlocks = [];
  const bt3 = String.fromCharCode(96, 96, 96);
  const codeBlockRegex = new RegExp(bt3 + '([a-zA-Z0-9_\\-\\+]*)\\n([\\s\\S]*?)' + bt3, 'g');
  let text = md.replace(codeBlockRegex, (match, lang, code) => {
    const idx = codeBlocks.length;
    codeBlocks.push({ lang: lang || 'text', code: code });
    return '___CODE_BLOCK_' + idx + '___';
  });

  // 2. Extract inline code
  const inlineCodes = [];
  const bt1 = String.fromCharCode(96);
  const inlineCodeRegex = new RegExp(bt1 + '([^' + bt1 + '\\n]+)' + bt1, 'g');
  text = text.replace(inlineCodeRegex, (match, code) => {
    const idx = inlineCodes.length;
    inlineCodes.push(code);
    return '___INLINE_CODE_' + idx + '___';
  });

  // 3. Helper to escape HTML safely
  function escapeHtml(str) {
    return str
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  function parseInline(str) {
    let s = escapeHtml(str);
    s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/__([^_]+)__/g, '<strong>$1</strong>');
    s = s.replace(/\*([^*]+)\*/g, '<em>$1</em>');
    s = s.replace(/_([^_]+)_/g, '<em>$1</em>');
    s = s.replace(/~~([^~]+)~~/g, '<del>$1</del>');
    s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
    return s;
  }

  // 4. Parse line by line
  const rawLines = text.split('\n');
  const out = [];
  let inList = false;
  let listType = '';
  let inBlockquote = false;
  let blockquoteType = '';
  let blockquoteLines = [];
  let inTable = false;
  let tableHeader = [];
  let tableRows = [];

  function closeList() {
    if (inList) {
      out.push(listType === 'ul' ? '</ul>' : '</ol>');
      inList = false;
      listType = '';
    }
  }

  function closeBlockquote() {
    if (inBlockquote) {
      const cls = blockquoteType ? ' ' + blockquoteType : '';
      out.push('<blockquote class="' + cls + '">' + blockquoteLines.join('<br>') + '</blockquote>');
      inBlockquote = false;
      blockquoteType = '';
      blockquoteLines = [];
    }
  }

  function closeTable() {
    if (inTable) {
      let tblHtml = '<table><thead><tr>';
      tableHeader.forEach(h => {
        tblHtml += '<th>' + parseInline(h) + '</th>';
      });
      tblHtml += '</tr></thead><tbody>';
      tableRows.forEach(row => {
        tblHtml += '<tr>';
        row.forEach(cell => {
          tblHtml += '<td>' + parseInline(cell) + '</td>';
        });
        tblHtml += '</tr>';
      });
      tblHtml += '</tbody></table>';
      out.push(tblHtml);
      inTable = false;
      tableHeader = [];
      tableRows = [];
    }
  }

  for (let i = 0; i < rawLines.length; i++) {
    const line = rawLines[i];
    const trimmed = line.trim();

    // Table rows
    if (trimmed.startsWith('|') && trimmed.endsWith('|')) {
      closeList();
      closeBlockquote();

      const cells = trimmed.slice(1, -1).split('|').map(c => c.trim());
      const isSep = cells.every(c => /^:?-+:?$/.test(c));

      if (isSep) {
        continue;
      }

      if (!inTable) {
        if (i + 1 < rawLines.length && rawLines[i + 1].trim().startsWith('|')) {
          const nextCells = rawLines[i + 1].trim().slice(1, -1).split('|').map(c => c.trim());
          if (nextCells.every(c => /^:?-+:?$/.test(c))) {
            inTable = true;
            tableHeader = cells;
            continue;
          }
        }
      }

      if (inTable) {
        tableRows.push(cells);
        continue;
      }
    } else {
      closeTable();
    }

    if (!trimmed) {
      closeList();
      closeBlockquote();
      continue;
    }

    // Horizontal rule
    if (/^(\*{3,}|-{3,}|_{3,})$/.test(trimmed)) {
      closeList();
      closeBlockquote();
      out.push('<hr>');
      continue;
    }

    // Headers
    const hMatch = trimmed.match(/^(#{1,6})\s+(.*)$/);
    if (hMatch) {
      closeList();
      closeBlockquote();
      const level = hMatch[1].length;
      out.push('<h' + level + '>' + parseInline(hMatch[2]) + '</h' + level + '>');
      continue;
    }

    // Blockquotes & Alerts
    if (trimmed.startsWith('>')) {
      closeList();
      let bqContent = trimmed.replace(/^>\s?/, '');
      const alertMatch = bqContent.match(/^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/i);
      if (alertMatch) {
        closeBlockquote();
        inBlockquote = true;
        const type = alertMatch[1].toLowerCase();
        blockquoteType = 'alert-' + type;
        const alertIcons = { note: 'ℹ️ NOTE', tip: '💡 TIP', important: '❗ IMPORTANT', warning: '⚠️ WARNING', caution: '🛑 CAUTION' };
        blockquoteLines.push('<strong>' + (alertIcons[type] || type.toUpperCase()) + '</strong>');
        continue;
      }
      if (!inBlockquote) {
        inBlockquote = true;
        blockquoteType = '';
      }
      blockquoteLines.push(parseInline(bqContent));
      continue;
    } else {
      closeBlockquote();
    }

    // List items
    const ulMatch = trimmed.match(/^[-*+]\s+(.*)$/);
    const olMatch = trimmed.match(/^(\d+)\.\s+(.*)$/);
    if (ulMatch || olMatch) {
      const isUl = !!ulMatch;
      const content = isUl ? ulMatch[1] : olMatch[2];
      const targetType = isUl ? 'ul' : 'ol';

      if (!inList || listType !== targetType) {
        closeList();
        inList = true;
        listType = targetType;
        out.push(isUl ? '<ul>' : '<ol>');
      }

      const taskMatch = content.match(/^\[([ xX])\]\s+(.*)$/);
      if (taskMatch) {
        const checked = taskMatch[1].toLowerCase() === 'x';
        out.push('<li><input type="checkbox" ' + (checked ? 'checked ' : '') + 'disabled> ' + parseInline(taskMatch[2]) + '</li>');
      } else {
        out.push('<li>' + parseInline(content) + '</li>');
      }
      continue;
    } else {
      closeList();
    }

    // Paragraph
    out.push('<p>' + parseInline(trimmed) + '</p>');
  }

  closeList();
  closeBlockquote();
  closeTable();

  let htmlResult = out.join('\n');

  // Restore inline codes
  inlineCodes.forEach((code, idx) => {
    htmlResult = htmlResult.replace('___INLINE_CODE_' + idx + '___', '<code>' + escapeHtml(code) + '</code>');
  });

  // Restore code blocks
  codeBlocks.forEach((item, idx) => {
    const langDisplay = item.lang.toUpperCase() || 'CODE';
    const blockHtml = '<div class="code-fence-wrapper"><div class="code-fence-header"><span>' + escapeHtml(langDisplay) + '</span></div><pre><code class="code-body">' + escapeHtml(item.code.trim()) + '</code></pre></div>';
    htmlResult = htmlResult.replace('___CODE_BLOCK_' + idx + '___', blockHtml);
  });

  return htmlResult;
}

function openFileViewer(path) {
  const content = fileData[path] || fileData[path.replace(/^\/+/, '')] || '';
  const absPath = filePaths[path] || filePaths[path.replace(/^\/+/, '')] || '';

  currentViewerFile = path;
  currentViewerContent = content;

  const viewerSection = document.getElementById('fileViewerSection');
  const fileNameEl = document.getElementById('viewerFileName');
  const iconEl = document.getElementById('viewerFileIcon');
  const badgeEl = document.getElementById('viewerBadge');
  const linesEl = document.getElementById('viewerLines');
  const rawLinkEl = document.getElementById('viewerRawLink');
  const copyBtn = document.getElementById('copyCodeBtn');
  const closeBtn = document.getElementById('closeViewerBtn');
  const emptyState = document.getElementById('viewerEmptyState');
  const contentEl = document.getElementById('viewerContent');
  const renderedEl = document.getElementById('viewerRendered');
  const tabsEl = document.getElementById('viewerTabs');
  const codeEl = document.getElementById('viewerCode');
  const lineNumbersEl = document.getElementById('lineNumbers');

  fileNameEl.textContent = path;

  if (!content) {
    emptyState.style.display = 'block';
    emptyState.innerHTML = '<div style="font-size:1.5rem; margin-bottom:0.5rem; color:var(--warning);">⚠️</div><div>Artifact file not found on disk: <code>' + path + '</code></div>';
    contentEl.style.display = 'none';
    renderedEl.style.display = 'none';
    tabsEl.style.display = 'none';
    badgeEl.style.display = 'none';
    linesEl.style.display = 'none';
    rawLinkEl.style.display = 'none';
    copyBtn.style.display = 'none';
    closeBtn.style.display = 'inline-flex';
    viewerSection.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    return;
  }

  emptyState.style.display = 'none';
  badgeEl.style.display = 'inline-flex';
  linesEl.style.display = 'inline-flex';
  copyBtn.style.display = 'inline-flex';
  closeBtn.style.display = 'inline-flex';

  const ext = path.split('.').pop().toLowerCase();
  const isMd = ext === 'md';
  badgeEl.textContent = ext.toUpperCase() + ' File';

  if (isMd) {
    iconEl.textContent = '📚';
    tabsEl.style.display = 'flex';
  } else {
    iconEl.textContent = '📄';
    tabsEl.style.display = 'none';
  }

  // Populate code body and lines for raw source
  codeEl.textContent = content;
  const lines = content.split('\n');
  linesEl.textContent = lines.length + ' lines';
  let nums = '';
  for (let i = 1; i <= lines.length; i++) {
    nums += i + '\n';
  }
  lineNumbersEl.textContent = nums;

  if (isMd) {
    switchViewerMode('rendered');
  } else {
    switchViewerMode('raw');
  }

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
  document.getElementById('viewerRendered').style.display = 'none';
  document.getElementById('viewerTabs').style.display = 'none';
  document.getElementById('viewerEmptyState').style.display = 'block';
  document.getElementById('viewerEmptyState').innerHTML = '<div style="font-size: 1.75rem; margin-bottom: 0.5rem; opacity: 0.6;">🔍</div><div>Click any specification document or verification test above to inspect its implementation on disk.</div>';
  document.getElementById('viewerFileName').textContent = 'Select a file above to view contents';
  document.getElementById('viewerBadge').style.display = 'none';
  document.getElementById('viewerLines').style.display = 'none';
  document.getElementById('viewerRawLink').style.display = 'none';
  document.getElementById('copyCodeBtn').style.display = 'none';
  document.getElementById('closeViewerBtn').style.display = 'none';
}

function copyViewerCode() {
  navigator.clipboard.writeText(currentViewerContent).then(() => {
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

// formatFileSize formats byte sizes into human readable strings.
func formatFileSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
