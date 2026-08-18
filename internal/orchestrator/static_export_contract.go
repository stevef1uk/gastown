package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// StaticExportReport describes potential mismatches between a frontend static-export
// build and backend static-file serving.
type StaticExportReport struct {
	HasFrontendBuild   bool
	HasStaticExport    bool
	HasStaticServing   bool
	HasFallbackPage    bool
	FrontendBuildDir   string
	BackendServePath   string
	Issues             []string
}

func (r StaticExportReport) IsClean() bool {
	return len(r.Issues) == 0
}

// DetectStaticExportAndServing scans a rig for frontend build config and backend
// static-file serving. It is intentionally generic across Next.js, Vite, and plain
// HTML frontends served by FastAPI/Flask/Express/Go. It discovers the layout
// root (e.g. `finally/`) so nested apps are checked, and it flags backends that
// serve a hardcoded fallback/placeholder page instead of the real UI.
func DetectStaticExportAndServing(mayorRigDir string) StaticExportReport {
	report := StaticExportReport{}

	frontendDir, backendDir := locateFrontendBackendDirs(mayorRigDir)

	// Frontend detection.
	if frontendDir != "" {
		report.HasFrontendBuild = true
		report.HasStaticExport, report.FrontendBuildDir = detectFrontendStaticExport(frontendDir)
	}

	// Backend detection.
	if backendDir != "" {
		report.HasStaticServing, report.BackendServePath = detectBackendStaticServing(backendDir)
		report.HasFallbackPage = detectBackendFallbackPage(backendDir)
	}

	if report.HasFallbackPage {
		report.Issues = append(report.Issues,
			"backend serves a hardcoded fallback/placeholder HTML page when the frontend export "+
				"is not found (e.g. `_FALLBACK_HTML`, a `Finally`/`Coming soon` page). This masks "+
				"build failures: the app renders the placeholder while QA smoke only sees HTTP 200. "+
				"Point the backend at the real static-export directory and serve `index.html` from it; "+
				"if the export is missing, fail loudly instead of returning a placeholder.")
	}
	if report.HasStaticExport && !report.HasStaticServing {
		report.Issues = append(report.Issues,
			"frontend builds a static export to "+report.FrontendBuildDir+
				", but backend has no static-file serving route. Serve the built files from the backend so the UI loads on the same origin.")
	}
	if report.HasStaticServing && !report.HasStaticExport {
		report.Issues = append(report.Issues,
			"backend has static-file serving configured, but no frontend static-export build was detected. Ensure the frontend build produces files in "+
				report.BackendServePath+".")
	}
	if report.HasStaticExport && report.HasStaticServing && report.FrontendBuildDir != "" && report.BackendServePath != "" {
		if !strings.Contains(report.BackendServePath, report.FrontendBuildDir) && !strings.Contains(report.FrontendBuildDir, report.BackendServePath) {
			report.Issues = append(report.Issues,
				"frontend static-export output directory ("+report.FrontendBuildDir+
					") does not match backend static-file serve directory ("+report.BackendServePath+").")
		}
	}

	return report
}

// locateFrontendBackendDirs finds the frontend/ and backend/ directories for the
// app, honoring a nested layout root (e.g. `finally/`). It checks the rig root
// first, then the layout root subdirectory, then falls back to walking the rig
// for the first directory literally named `frontend`/`backend`.
func locateFrontendBackendDirs(mayorRigDir string) (frontendDir, backendDir string) {
	if d := filepath.Join(mayorRigDir, "frontend"); isDir(d) {
		frontendDir = d
	}
	if d := filepath.Join(mayorRigDir, "backend"); isDir(d) {
		backendDir = d
	}
	if frontendDir != "" && backendDir != "" {
		return frontendDir, backendDir
	}

	// Nested layout: a single subdirectory holding the whole app (finally/, app/, src/).
	entries, err := os.ReadDir(mayorRigDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == ".git" || e.Name() == "node_modules" {
				continue
			}
			if frontendDir == "" {
				if d := filepath.Join(mayorRigDir, e.Name(), "frontend"); isDir(d) {
					frontendDir = d
				}
			}
			if backendDir == "" {
				if d := filepath.Join(mayorRigDir, e.Name(), "backend"); isDir(d) {
					backendDir = d
				}
			}
		}
	}
	if frontendDir != "" && backendDir != "" {
		return frontendDir, backendDir
	}

	// Deep fallback: walk the rig for literal frontend/backend dirs.
	if frontendDir == "" {
		frontendDir = findDirNamed(mayorRigDir, "frontend")
	}
	if backendDir == "" {
		backendDir = findDirNamed(mayorRigDir, "backend")
	}
	return frontendDir, backendDir
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func findDirNamed(root, name string) string {
	found := ""
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == ".venv" {
			return filepath.SkipDir
		}
		if d.Name() == name && found == "" {
			found = path
			return filepath.SkipDir
		}
		return nil
	})
	return found
}

// detectBackendFallbackPage reports whether the backend contains a hardcoded
// placeholder HTML page served when the frontend export is missing. It scans
// source files in any language (Python, Go, JS/TS) for fallback HTML constants
// or routes that return a fixed placeholder string.
func detectBackendFallbackPage(backendDir string) bool {
	found := false
	_ = filepath.Walk(backendDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !isSourceFile(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		if strings.Contains(content, "_FALLBACK_HTML") || strings.Contains(content, "FALLBACK_HTML") ||
			strings.Contains(content, "PLACEHOLDER_HTML") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

var sourceExts = map[string]bool{
	".py": true, ".go": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
}

func isSourceFile(path string) bool {
	base := filepath.Base(path)
	lower := strings.ToLower(filepath.ToSlash(path))
	if strings.Contains(lower, "/node_modules/") || strings.Contains(lower, "/.venv/") {
		return false
	}
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "_test.go") ||
		strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return false
	}
	return sourceExts[strings.ToLower(filepath.Ext(path))]
}

func detectFrontendStaticExport(frontendDir string) (bool, string) {
	buildDir := ""
	if data, err := os.ReadFile(filepath.Join(frontendDir, "next.config.js")); err == nil {
		content := string(data)
		if strings.Contains(content, "output:") && strings.Contains(content, "'export'") ||
			strings.Contains(content, "output:") && strings.Contains(content, `"export"`) {
			// distDir may be configured.
			re := regexp.MustCompile(`distDir\s*:\s*['"]([^'"]+)['"]`)
			if m := re.FindStringSubmatch(content); len(m) > 1 {
				buildDir = m[1]
			} else {
				buildDir = "out"
			}
			return true, buildDir
		}
	}
	if data, err := os.ReadFile(filepath.Join(frontendDir, "next.config.ts")); err == nil {
		content := string(data)
		if strings.Contains(content, "output:") && strings.Contains(content, "export") {
			re := regexp.MustCompile(`distDir\s*:\s*['"]([^'"]+)['"]`)
			if m := re.FindStringSubmatch(content); len(m) > 1 {
				buildDir = m[1]
			} else {
				buildDir = "out"
			}
			return true, buildDir
		}
	}
	if _, err := os.ReadFile(filepath.Join(frontendDir, "vite.config.js")); err == nil {
		return true, "dist"
	}
	if _, err := os.ReadFile(filepath.Join(frontendDir, "vite.config.ts")); err == nil {
		return true, "dist"
	}
	return false, ""
}

func detectBackendStaticServing(backendDir string) (bool, string) {
	servePath := ""
	found := false
	_ = filepath.Walk(backendDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		lower := strings.ToLower(content)

		// FastAPI StaticFiles
		if re := regexp.MustCompile(`StaticFiles\s*\(\s*directory\s*=\s*["']([^"']+)["']`); re.MatchString(content) {
			found = true
			m := re.FindStringSubmatch(content)
			if len(m) > 1 {
				servePath = m[1]
			}
		}
		// Flask send_from_directory
		if re := regexp.MustCompile(`send_from_directory\s*\(\s*["']([^"']+)["']`); re.MatchString(content) {
			found = true
			m := re.FindStringSubmatch(content)
			if len(m) > 1 {
				servePath = m[1]
			}
		}
		// Go http.FileServer / fs.FS
		if strings.Contains(lower, "http.fileserver") || strings.Contains(lower, "http.fsf") {
			found = true
			re := regexp.MustCompile(`http\.Dir\s*\(\s*["']([^"']+)["']\)`)
			if m := re.FindStringSubmatch(content); len(m) > 1 {
				servePath = m[1]
			}
		}
		// Express static
		if re := regexp.MustCompile(`express\.static\s*\(\s*["']([^"']+)["']`); re.MatchString(content) {
			found = true
			m := re.FindStringSubmatch(content)
			if len(m) > 1 {
				servePath = m[1]
			}
		}
		return nil
	})
	return found, servePath
}

// FormatStaticExportGuidance returns QA guidance from a StaticExportReport, or empty if clean.
func FormatStaticExportGuidance(report StaticExportReport) string {
	if report.IsClean() {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Static export / static serving consistency\n")
	b.WriteString("The frontend and backend static-file wiring looks inconsistent. Fix before QA passes:\n\n")
	for _, issue := range report.Issues {
		b.WriteString("- " + issue + "\n")
	}
	return strings.TrimSpace(b.String())
}
