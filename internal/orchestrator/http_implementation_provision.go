package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nonStdlibHTTPStackRE = regexp.MustCompile(`(?i)(github\.com/go-chi/chi|labstack/echo|gin-gonic/gin|gorilla/mux|gofiber/fiber|valyala/fasthttp|\becho\.New\(|\bchi\.NewRouter\()`)

// HTTPImplementationRigConfigPath is the on-disk rig override (auto-created when needed).
func HTTPImplementationRigConfigPath(townRoot, rig string) string {
	return filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, httpProfileRigFile)
}

// NeedsHTTPImplementationProfile reports whether this rig should have a rig-local HTTP profile file.
func NeedsHTTPImplementationProfile(townRoot, rig string, v WorkflowValidation) bool {
	if !WorkflowUsesGo(v) {
		return false
	}
	if WorkflowNeedsRuntimeSmoke(v) {
		return true
	}
	if m := LoadWebStaticMappingFromRig(townRoot, rig, v); m.StaticURLPrefix != "" {
		return true
	}
	return workflowRequiredFilesSuggestHTTP(v)
}

func workflowRequiredFilesSuggestHTTP(v WorkflowValidation) bool {
	files := append([]string(nil), v.RequiredFiles...)
	files = append(files, v.UnionRequiredFiles()...)
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.Contains(f, "handlers.go") || strings.Contains(f, "/internal/api/") {
			return true
		}
	}
	return false
}

func architectureSuggestsNonStdlibHTTP(townRoot, rig string) bool {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	for _, name := range []string{"architecture.md", "SPEC.md"} {
		data, err := os.ReadFile(filepath.Join(rigDir, name))
		if err != nil {
			continue
		}
		if nonStdlibHTTPStackRE.Match(data) {
			return true
		}
	}
	return false
}

// SelectHTTPImplementationProfileID picks a town profile name from workflow + rig docs (no LLM).
func SelectHTTPImplementationProfileID(townRoot, rig string, v WorkflowValidation) string {
	if !WorkflowUsesGo(v) {
		return "generic"
	}
	if architectureSuggestsNonStdlibHTTP(townRoot, rig) {
		return "generic"
	}
	if WorkflowNeedsRuntimeSmoke(v) {
		return defaultHTTPProfileID
	}
	m := LoadWebStaticMappingFromRig(townRoot, rig, v)
	if m.StaticURLPrefix != "" || workflowRequiredFilesSuggestHTTP(v) {
		return defaultHTTPProfileID
	}
	return "generic"
}

func deterministicHTTPImplementationOverrides(townRoot, rig string, v WorkflowValidation) *httpRigConfigOverrides {
	m := LoadWebStaticMappingFromRig(townRoot, rig, v)
	prefix := strings.TrimSuffix(strings.TrimSpace(m.StaticURLPrefix), "/")
	if prefix == "" {
		return nil
	}
	return &httpRigConfigOverrides{TraversalProbePath: prefix + "/../go.mod"}
}

type httpRigConfigFile struct {
	Profile   string                 `json:"profile"`
	Overrides *httpRigConfigOverrides `json:"overrides,omitempty"`
}

type httpRigConfigOverrides struct {
	TraversalProbePath string `json:"traversal_probe_path,omitempty"`
}

func buildHTTPImplementationRigConfigFile(townRoot, rig string, v WorkflowValidation) httpRigConfigFile {
	return httpRigConfigFile{
		Profile:   SelectHTTPImplementationProfileID(townRoot, rig, v),
		Overrides: deterministicHTTPImplementationOverrides(townRoot, rig, v),
	}
}

// EnsureHTTPImplementationRigConfig creates mayor/rig/.gastown/http-implementation.json when missing.
// Existing files are never overwritten (operators may tweak in place).
func EnsureHTTPImplementationRigConfig(townRoot, rig string, v WorkflowValidation) (created bool, err error) {
	if townRoot == "" || rig == "" || !NeedsHTTPImplementationProfile(townRoot, rig, v) {
		return false, nil
	}
	_ = InstallDefaultHTTPProfiles(townRoot)
	path := HTTPImplementationRigConfigPath(townRoot, rig)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	cfg := buildHTTPImplementationRigConfigFile(townRoot, rig, v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return false, err
	}
	invalidateHTTPProfileCache()
	return true, nil
}

// EnsureHTTPImplementationRigConfigLog runs EnsureHTTPImplementationRigConfig for rig-flow hooks.
func EnsureHTTPImplementationRigConfigLog(townRoot, rig string, v WorkflowValidation) (string, error) {
	created, err := EnsureHTTPImplementationRigConfig(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if !created {
		return "", nil
	}
	cfg := buildHTTPImplementationRigConfigFile(townRoot, rig, v)
	rel := filepath.ToSlash(filepath.Join(rig, "mayor", "rig", rigProfileDir, httpProfileRigFile))
	return fmt.Sprintf("wrote %s (profile %q from SPEC/architecture — edit to override stack guards)", rel, cfg.Profile), nil
}

func invalidateHTTPProfileCache() {
	httpProfileCache.Lock()
	httpProfileCache.key = ""
	httpProfileCache.Unlock()
}
