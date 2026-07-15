package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// APIEndpoint describes a backend route.
type APIEndpoint struct {
	Method string
	Path   string
	File   string
}

// APICall describes a frontend request to a backend endpoint.
type APICall struct {
	Method string
	Path   string
	File   string
}

// APIContractReport lists mismatches between frontend API consumers and backend routes.
type APIContractReport struct {
	MissingBackend []APICall   // frontend calls with no matching backend route
	MissingFrontend []APIEndpoint // backend routes with no matching frontend call
}

func (r APIContractReport) IsClean() bool {
	return len(r.MissingBackend) == 0 && len(r.MissingFrontend) == 0
}

func (r APIContractReport) String() string {
	var b strings.Builder
	for _, c := range r.MissingBackend {
		fmt.Fprintf(&b, "frontend calls %s %s (%s) but backend has no matching route\n", c.Method, c.Path, c.File)
	}
	for _, e := range r.MissingFrontend {
		fmt.Fprintf(&b, "backend exposes %s %s (%s) but no frontend caller was found\n", e.Method, e.Path, e.File)
	}
	return strings.TrimSpace(b.String())
}

// ExtractFrontendAPICalls scans JavaScript/TypeScript content for calls to /api/* paths.
func ExtractFrontendAPICalls(content, file string) []APICall {
	var calls []APICall
	// fetch('/api/foo', { method: 'POST' })
	fetchRE := regexp.MustCompile(`(?i)fetch\s*\(\s*['"]([^'"]+)['"]\s*(?:,\s*\{([^}]*)\})?`)
	for _, m := range fetchRE.FindAllStringSubmatch(content, -1) {
		path := m[1]
		method := "GET"
		if len(m) > 2 && m[2] != "" {
			method = extractMethod(m[2])
		}
		if isAPIPath(path) {
			calls = append(calls, APICall{Method: method, Path: normalizePath(path), File: file})
		}
	}

	// axios.get('/api/foo') or httpClient.post('/api/foo')
	verbRE := regexp.MustCompile(`(?i)\.(get|post|put|delete|patch)\s*\(\s*['"]([^'"]+)['"]`)
	for _, m := range verbRE.FindAllStringSubmatch(content, -1) {
		method := strings.ToUpper(m[1])
		path := m[2]
		if isAPIPath(path) {
			calls = append(calls, APICall{Method: method, Path: normalizePath(path), File: file})
		}
	}

	return calls
}

// ExtractBackendAPIRoutes scans backend content for common route definitions.
func ExtractBackendAPIRoutes(content, file string) []APIEndpoint {
	var routes []APIEndpoint
	ext := filepath.Ext(strings.ToLower(file))

	switch {
	case ext == ".py":
		routes = append(routes, extractPythonRoutes(content, file)...)
	case ext == ".go":
		routes = append(routes, extractGoRoutes(content, file)...)
	case ext == ".js" || ext == ".ts":
		routes = append(routes, extractNodeRoutes(content, file)...)
	}

	return routes
}

func extractPythonRoutes(content, file string) []APIEndpoint {
	var routes []APIEndpoint
	// FastAPI: @router.get("/path") or @app.post("/path")
	fastapiRE := regexp.MustCompile(`(?i)@\s*(?:\w+\.)?(get|post|put|delete|patch)\s*\(\s*["']([^"']+)["']`)
	for _, m := range fastapiRE.FindAllStringSubmatch(content, -1) {
		routes = append(routes, APIEndpoint{Method: strings.ToUpper(m[1]), Path: normalizePath(m[2]), File: file})
	}
	// Flask: @app.route("/path", methods=["POST"])
	flaskRE := regexp.MustCompile(`(?i)@\s*\w+\.route\s*\(\s*["']([^"']+)["'](?:[^\)]*methods\s*=\s*\[([^\]]*)\])?`)
	for _, m := range flaskRE.FindAllStringSubmatch(content, -1) {
		method := "GET"
		if len(m) > 2 && m[2] != "" {
			method = firstMethod(m[2])
		}
		routes = append(routes, APIEndpoint{Method: method, Path: normalizePath(m[1]), File: file})
	}
	return routes
}

func extractGoRoutes(content, file string) []APIEndpoint {
	var routes []APIEndpoint
	// http.HandleFunc("/path", handler)
	handleRE := regexp.MustCompile(`(?i)HandleFunc\s*\(\s*["']([^"']+)["']`)
	for _, m := range handleRE.FindAllStringSubmatch(content, -1) {
		routes = append(routes, APIEndpoint{Method: "GET", Path: normalizePath(m[1]), File: file})
	}
	// router.GET("/path", ...)
	verbRE := regexp.MustCompile(`(?i)\.(Get|Post|Put|Delete|Patch)\s*\(\s*["']([^"']+)["']`)
	for _, m := range verbRE.FindAllStringSubmatch(content, -1) {
		routes = append(routes, APIEndpoint{Method: strings.ToUpper(m[1]), Path: normalizePath(m[2]), File: file})
	}
	return routes
}

func extractNodeRoutes(content, file string) []APIEndpoint {
	var routes []APIEndpoint
	verbRE := regexp.MustCompile(`(?i)app\.(get|post|put|delete|patch)\s*\(\s*["']([^"']+)["']`)
	for _, m := range verbRE.FindAllStringSubmatch(content, -1) {
		routes = append(routes, APIEndpoint{Method: strings.ToUpper(m[1]), Path: normalizePath(m[2]), File: file})
	}
	return routes
}

func isAPIPath(path string) bool {
	path = strings.TrimSpace(path)
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "api/")
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Split(path, "?")[0]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func extractMethod(opts string) string {
	re := regexp.MustCompile(`(?i)method\s*:\s*['"](\w+)['"]`)
	m := re.FindStringSubmatch(opts)
	if len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return "GET"
}

func firstMethod(methods string) string {
	re := regexp.MustCompile(`['"](\w+)['"]`)
	m := re.FindStringSubmatch(methods)
	if len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return "GET"
}

// MatchAPIContract compares frontend calls against backend routes and reports mismatches.
func MatchAPIContract(calls []APICall, routes []APIEndpoint) APIContractReport {
	var report APIContractReport

	routeSet := make(map[string]bool)
	for _, r := range routes {
		routeSet[key(r.Method, r.Path)] = true
	}

	callSet := make(map[string]bool)
	for _, c := range calls {
		callSet[key(c.Method, c.Path)] = true
	}

	for _, c := range calls {
		if !routeSet[key(c.Method, c.Path)] {
			report.MissingBackend = append(report.MissingBackend, c)
		}
	}

	for _, r := range routes {
		if !callSet[key(r.Method, r.Path)] {
			report.MissingFrontend = append(report.MissingFrontend, r)
		}
	}

	return report
}

func key(method, path string) string {
	return strings.ToUpper(method) + " " + path
}

// LoadAPIContractFromRig scans frontend and backend files under mayorRigDir and returns a contract report.
func LoadAPIContractFromRig(mayorRigDir string) (APIContractReport, error) {
	var calls []APICall
	var routes []APIEndpoint

	if err := filepath.Walk(mayorRigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(mayorRigDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		// Skip node_modules, .git, and test directories.
		lower := strings.ToLower(rel)
		if strings.Contains(lower, "node_modules") || strings.Contains(lower, "/.git/") || strings.Contains(rel, "test/") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // ignore unreadable files
		}
		content := string(data)

		ext := strings.ToLower(filepath.Ext(rel))
		if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
			calls = append(calls, ExtractFrontendAPICalls(content, rel)...)
		}
		if ext == ".py" || ext == ".go" || ext == ".js" || ext == ".ts" {
			routes = append(routes, ExtractBackendAPIRoutes(content, rel)...)
		}
		return nil
	}); err != nil {
		return APIContractReport{}, err
	}

	return MatchAPIContract(calls, routes), nil
}

// FormatAPIContractGuidance returns a QA guidance block from a contract report, or empty if clean.
func FormatAPIContractGuidance(report APIContractReport) string {
	if report.IsClean() {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Cross-stack API contract issues\n")
	b.WriteString("The frontend and backend disagree on the API surface. Fix these before marking QA passed:\n\n")
	for _, c := range report.MissingBackend {
		fmt.Fprintf(&b, "- Frontend calls `%s %s` in `%s`, but no matching backend route exists.\n", c.Method, c.Path, c.File)
	}
	for _, r := range report.MissingFrontend {
		fmt.Fprintf(&b, "- Backend exposes `%s %s` in `%s`, but no frontend caller was found.\n", r.Method, r.Path, r.File)
	}
	return strings.TrimSpace(b.String())
}
