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
	FrontendBuildDir   string
	BackendServePath   string
	Issues             []string
}

func (r StaticExportReport) IsClean() bool {
	return len(r.Issues) == 0
}

// DetectStaticExportAndServing scans a rig for frontend build config and backend
// static-file serving. It is intentionally generic across Next.js, Vite, and plain
// HTML frontends served by FastAPI/Flask/Express/Go.
func DetectStaticExportAndServing(mayorRigDir string) StaticExportReport {
	report := StaticExportReport{}

	// Frontend detection.
	frontendDir := filepath.Join(mayorRigDir, "frontend")
	if _, err := os.Stat(frontendDir); err == nil {
		report.HasFrontendBuild = true
		report.HasStaticExport, report.FrontendBuildDir = detectFrontendStaticExport(frontendDir)
	}

	// Backend detection.
	backendDir := filepath.Join(mayorRigDir, "backend")
	if _, err := os.Stat(backendDir); err == nil {
		report.HasStaticServing, report.BackendServePath = detectBackendStaticServing(backendDir)
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
				buildDir = "dist"
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
				buildDir = "dist"
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
