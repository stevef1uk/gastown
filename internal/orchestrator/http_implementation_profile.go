package orchestrator

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

//go:embed httpprofiles/defaults/*.json
var embeddedHTTPProfiles embed.FS

const (
	httpProfileRigFile     = "http-implementation.json"
	httpProfilesTownSubdir = "http-profiles"
	defaultHTTPProfileID   = "go-stdlib-servemux"
)

// HTTPImplementationProfile configures stack-specific handler guards and verify hints (GT-VERIFY-011).
// Edit town orchestrator/http-profiles/*.json or rig .gastown/http-implementation.json — no gt-agent rebuild.
type HTTPImplementationProfile struct {
	ID                       string                       `json:"id"`
	Stack                    string                       `json:"stack"`
	Enabled                  bool                         `json:"enabled"`
	WebDiskDir               string                       `json:"web_disk_dir"`
	HandlerPathSubstrings    []string                     `json:"handler_path_substrings"`
	TestPathSuffix           string                       `json:"test_path_suffix"`
	WriteGuards              []HTTPWriteGuard             `json:"write_guards"`
	TestOutputMatchers       map[string][]string          `json:"test_output_matchers"`
	Hints                    HTTPImplementationHints      `json:"hints"`
	ImplementGuidance        HTTPImplementGuidance        `json:"implement_guidance"`
	TraversalProbePath       string                       `json:"traversal_probe_path"`
	StaticRouteRegisterPrefix string                      `json:"static_route_register_prefix"`
	EarlyTraversalFields     []string                     `json:"early_traversal_fields"`
	StaticURLPrefix          string                       `json:"-"` // merged from architecture at load
	compiled                 *compiledHTTPProfile         `json:"-"`
}

type HTTPWriteGuard struct {
	ID                      string `json:"id"`
	HandlerSourceRegex      string `json:"handler_source_regex"`
	UnlessRegex             string `json:"unless_regex"`
	RequiresRouteRegex      string `json:"requires_route_regex"`
	WhenStaticPrefix        string `json:"when_static_prefix"`
	StaticHandlerBlockGuard bool   `json:"static_handler_block_guard"`
	Message                 string `json:"message"`
}

type HTTPImplementationHints struct {
	TraversalRedirect HTTPHintBlock `json:"traversal_redirect"`
	TestCwd           HTTPHintBlock `json:"test_cwd"`
	Root404ServeHelper HTTPHintBlock `json:"root_404_serve_helper"`
}

type HTTPHintBlock struct {
	Title              string   `json:"title"`
	Cause              string   `json:"cause"`
	Fixes              []string `json:"fixes"`
	FixesHandlersBead  []string `json:"fixes_handlers_bead"`
	FixesTestBead      []string `json:"fixes_test_bead"`
	ChdirLine          string   `json:"chdir_line"`
	Footer             string   `json:"footer"`
}

type HTTPImplementGuidance struct {
	Common      []string `json:"common"`
	HandlerBead []string `json:"handler_bead"`
	TestBead    []string `json:"test_bead"`
}

// HTTPImplementationRigConfig is the rig-local override file (profile name + patches).
type HTTPImplementationRigConfig struct {
	Profile  string                 `json:"profile"`
	Enabled  *bool                  `json:"enabled,omitempty"`
	Overrides HTTPImplementationProfile `json:"overrides,omitempty"`
}

type compiledHTTPProfile struct {
	guards []compiledGuard
}

type compiledGuard struct {
	whenStaticPrefix string
	handlerRE        *regexp.Regexp
	unlessRE         *regexp.Regexp
	requiresRouteRE  *regexp.Regexp
	blockGuard       bool
	message          string
}

var httpProfileCache struct {
	sync.Mutex
	key string
	prof HTTPImplementationProfile
}

// LoadHTTPImplementationProfile resolves stack config: embedded/town JSON + rig overrides + architecture static mapping.
func LoadHTTPImplementationProfile(townRoot, rig string, v WorkflowValidation) HTTPImplementationProfile {
	cacheKey := townRoot + "|" + rig + "|" + v.LayoutRoot
	httpProfileCache.Lock()
	if httpProfileCache.key == cacheKey {
		p := httpProfileCache.prof
		httpProfileCache.Unlock()
		return p
	}
	httpProfileCache.Unlock()

	mapping := LoadWebStaticMappingFromRig(townRoot, rig, v)
	prof := resolveHTTPImplementationProfile(townRoot, rig)
	prof.StaticURLPrefix = strings.TrimSpace(mapping.StaticURLPrefix)
	if prof.StaticURLPrefix == "" && mapping.RootServeStatic {
		prof.StaticURLPrefix = ""
	}
	if prof.TraversalProbePath == "" && prof.StaticURLPrefix != "" {
		prof.TraversalProbePath = prof.StaticURLPrefix + "/../go.mod"
	}
	prof.compile()

	httpProfileCache.Lock()
	httpProfileCache.key = cacheKey
	httpProfileCache.prof = prof
	httpProfileCache.Unlock()
	return prof
}

func resolveHTTPImplementationProfile(townRoot, rig string) HTTPImplementationProfile {
	cfg := loadHTTPImplementationRigConfig(townRoot, rig)
	name := defaultHTTPProfileID
	if cfg.Profile != "" {
		name = cfg.Profile
	}
	base, err := loadHTTPProfileByName(townRoot, name)
	if err != nil {
		base = mustEmbeddedHTTPProfile(defaultHTTPProfileID)
	}
	if cfg.Enabled != nil {
		base.Enabled = *cfg.Enabled
	}
	mergeHTTPProfileOverrides(&base, cfg.Overrides)
	if base.ID == "" {
		base.ID = name
	}
	return base
}

func loadHTTPImplementationRigConfig(townRoot, rig string) HTTPImplementationRigConfig {
	var cfg HTTPImplementationRigConfig
	if rig != "" && townRoot != "" {
		path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, httpProfileRigFile)
		data, err := os.ReadFile(path)
		if err == nil {
			_ = json.Unmarshal(data, &cfg)
		}
	}
	return cfg
}

func loadHTTPProfileByName(townRoot, name string) (HTTPImplementationProfile, error) {
	if name == "" {
		name = defaultHTTPProfileID
	}
	if townRoot != "" {
		path := filepath.Join(townRoot, "orchestrator", httpProfilesTownSubdir, name+".json")
		data, err := os.ReadFile(path)
		if err == nil {
			var prof HTTPImplementationProfile
			if json.Unmarshal(data, &prof) == nil {
				return prof, nil
			}
		}
	}
	return loadEmbeddedHTTPProfile(name)
}

func loadEmbeddedHTTPProfile(name string) (HTTPImplementationProfile, error) {
	data, err := embeddedHTTPProfiles.ReadFile("httpprofiles/defaults/" + name + ".json")
	if err != nil {
		return HTTPImplementationProfile{}, err
	}
	var prof HTTPImplementationProfile
	if err := json.Unmarshal(data, &prof); err != nil {
		return HTTPImplementationProfile{}, err
	}
	return prof, nil
}

func mustEmbeddedHTTPProfile(name string) HTTPImplementationProfile {
	p, err := loadEmbeddedHTTPProfile(name)
	if err != nil {
		return HTTPImplementationProfile{ID: "generic", Enabled: false, WebDiskDir: "web"}
	}
	return p
}

func mergeHTTPProfileOverrides(base *HTTPImplementationProfile, o HTTPImplementationProfile) {
	if o.WebDiskDir != "" {
		base.WebDiskDir = o.WebDiskDir
	}
	if o.TraversalProbePath != "" {
		base.TraversalProbePath = o.TraversalProbePath
	}
	if len(o.WriteGuards) > 0 {
		base.WriteGuards = o.WriteGuards
	}
	if len(o.TestOutputMatchers) > 0 {
		base.TestOutputMatchers = o.TestOutputMatchers
	}
	if o.Hints.TraversalRedirect.Title != "" || len(o.Hints.TraversalRedirect.Fixes) > 0 {
		base.Hints.TraversalRedirect = o.Hints.TraversalRedirect
	}
	if o.Hints.TestCwd.Title != "" || len(o.Hints.TestCwd.Fixes) > 0 {
		base.Hints.TestCwd = o.Hints.TestCwd
	}
	if o.Hints.Root404ServeHelper.Title != "" {
		base.Hints.Root404ServeHelper = o.Hints.Root404ServeHelper
	}
	if len(o.ImplementGuidance.Common) > 0 {
		base.ImplementGuidance.Common = o.ImplementGuidance.Common
	}
	if len(o.ImplementGuidance.HandlerBead) > 0 {
		base.ImplementGuidance.HandlerBead = o.ImplementGuidance.HandlerBead
	}
	if len(o.ImplementGuidance.TestBead) > 0 {
		base.ImplementGuidance.TestBead = o.ImplementGuidance.TestBead
	}
}

func (p *HTTPImplementationProfile) compile() {
	c := &compiledHTTPProfile{}
	for _, g := range p.WriteGuards {
		cg := compiledGuard{
			whenStaticPrefix: g.WhenStaticPrefix,
			blockGuard:       g.StaticHandlerBlockGuard,
			message:          g.Message,
		}
		if g.HandlerSourceRegex != "" {
			cg.handlerRE = regexp.MustCompile(g.HandlerSourceRegex)
		}
		if g.UnlessRegex != "" {
			cg.unlessRE = regexp.MustCompile(g.UnlessRegex)
		}
		if g.RequiresRouteRegex != "" {
			cg.requiresRouteRE = regexp.MustCompile(g.RequiresRouteRegex)
		}
		c.guards = append(c.guards, cg)
	}
	p.compiled = c
}

// ProfileAppliesToHandlerPath reports whether this profile governs the given implement path.
func (p HTTPImplementationProfile) ProfileAppliesToHandlerPath(relPath string) bool {
	if !p.Enabled {
		return false
	}
	rel := filepath.ToSlash(strings.TrimSpace(relPath))
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	for _, sub := range p.HandlerPathSubstrings {
		if sub != "" && strings.Contains(rel, sub) {
			return true
		}
	}
	return IsHTTPHandlerImplementPath(rel) || IsHTTPHandlerTestPath(rel)
}

func (p HTTPImplementationProfile) vars(beadPath string, v WorkflowValidation) map[string]string {
	prefix := strings.TrimSpace(p.StaticURLPrefix)
	if prefix == "" {
		prefix = "/static"
	}
	pattern := prefix
	if pattern != "" && !strings.HasSuffix(pattern, "/") {
		pattern += "/"
	}
	web := strings.TrimSpace(p.WebDiskDir)
	if web == "" {
		web = "web"
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "<module>"
	}
	testPath := handlerTestPathForHints(beadPath, "", v)
	return map[string]string{
		"web_disk_dir":       web,
		"static_prefix":      prefix,
		"static_pattern":     pattern,
		"traversal_probe_path": strings.TrimSpace(p.TraversalProbePath),
		"layout_root":        layout,
		"chdir_expr":         ChdirExprToModuleRootFromTest(testPath, v.LayoutRoot),
	}
}

func expandProfileTemplate(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

func (p HTTPImplementationProfile) expandLines(lines []string, vars map[string]string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, expandProfileTemplate(line, vars))
	}
	return out
}

// HandlerWriteGuardIssues runs profile-configured write-time checks on handler source.
func (p HTTPImplementationProfile) HandlerWriteGuardIssues(body string) []string {
	if !p.Enabled || p.compiled == nil {
		return nil
	}
	var issues []string
	prefix := strings.TrimSpace(p.StaticURLPrefix)
	for _, g := range p.compiled.guards {
		if g.whenStaticPrefix != "" && g.whenStaticPrefix != prefix && prefix != "" {
			if g.whenStaticPrefix != "/static" || !strings.HasPrefix(prefix, "/static") {
				continue
			}
		}
		if g.blockGuard {
			if !strings.Contains(body, p.StaticRouteRegisterPrefix) {
				continue
			}
			if !handlerStaticHandlerHasEarlyRequestURIGuard(body, p.StaticRouteRegisterPrefix, p.EarlyTraversalFields) {
				issues = append(issues, g.message)
			}
			continue
		}
		if g.handlerRE != nil && !g.handlerRE.MatchString(body) {
			continue
		}
		if g.unlessRE != nil && g.unlessRE.MatchString(body) {
			continue
		}
		if g.requiresRouteRE != nil && !g.requiresRouteRE.MatchString(body) {
			continue
		}
		issues = append(issues, g.message)
	}
	return issues
}

func handlerStaticHandlerHasEarlyRequestURIGuard(body, routePrefix string, fields []string) bool {
	block := handlerStaticHandlerBlock(body, routePrefix)
	if block == "" {
		return true
	}
	// Accept any line that contains both r.URL (Path/RequestURI/RawPath) and a ".." check.
	// Common patterns: if strings.Contains(r.URL.Path, "..") or r.URL.RequestURI() then ".."
	for _, line := range strings.Split(block, "\n") {
		l := strings.TrimSpace(line)
		if (strings.Contains(l, "r.URL") || strings.Contains(l, "RequestURI") || strings.Contains(l, "RawPath")) &&
			(strings.Contains(l, `".."`) || strings.Contains(l, `'..'`)) {
			return true
		}
	}
	for _, field := range fields {
		if !strings.Contains(block, field) {
			continue
		}
		idx := strings.Index(block, field)
		sub := block[idx:]
		if len(sub) > 400 {
			sub = sub[:400]
		}
		if strings.Contains(sub, `".."`) || strings.Contains(sub, `'..'`) {
			return true
		}
	}
	return false
}

func handlerStaticHandlerBlock(body, routePrefix string) string {
	idx := strings.Index(body, routePrefix)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(routePrefix):]
	comma := strings.Index(rest, ",")
	if comma >= 0 && comma < 50 {
		afterComma := strings.TrimSpace(rest[comma+1:])
		closeParen := strings.Index(afterComma, ")")
		if !strings.HasPrefix(afterComma, "func") && closeParen > 0 {
			handlerName := strings.TrimSpace(afterComma[:closeParen])
			if !strings.ContainsAny(handlerName, " \t\n\r(){}") {
				funcDef := "func " + handlerName
				funcIdx := strings.Index(body, funcDef)
				if funcIdx >= 0 {
					rest = body[funcIdx+len(funcDef):]
				}
			}
		}
	}
	brace := strings.Index(rest, "{")
	if brace < 0 {
		if len(rest) > 280 {
			rest = rest[:280]
		}
		return rest
	}
	depth := 0
	for i := brace; i < len(rest); i++ {
		switch rest[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[brace : i+1]
			}
		}
	}
	if len(rest) > brace+320 {
		return rest[brace : brace+320]
	}
	return rest[brace:]
}

func (p HTTPImplementationProfile) testOutputMatches(key, cmdOutput string) bool {
	needles, ok := p.TestOutputMatchers[key]
	if !ok || len(needles) == 0 {
		return false
	}
	for _, n := range needles {
		if !strings.Contains(cmdOutput, n) {
			return false
		}
	}
	return true
}

func (p HTTPImplementationProfile) GoTestOutputSuggestsTraversalRedirect(cmdOutput string) bool {
	if !goTestOutputSuggestsFailure(cmdOutput) {
		return false
	}
	if p.Enabled && len(p.TestOutputMatchers["traversal_redirect"]) > 0 {
		return p.testOutputMatches("traversal_redirect", cmdOutput)
	}
	return GoTestOutputSuggestsTraversalRedirectLegacy(cmdOutput)
}

func GoTestOutputSuggestsTraversalRedirectLegacy(cmdOutput string) bool {
	lower := strings.ToLower(cmdOutput)
	if strings.Contains(lower, "traversal request returned 307") {
		return true
	}
	return strings.Contains(lower, "traversal") &&
		strings.Contains(lower, "307") &&
		strings.Contains(lower, "404")
}

func (p HTTPImplementationProfile) GoTestOutputSuggestsHandlerWebCwd404(cmdOutput string) bool {
	if !goTestOutputSuggestsFailure(cmdOutput) {
		return false
	}
	if p.Enabled && len(p.TestOutputMatchers["root_404"]) > 0 {
		return p.testOutputMatches("root_404", cmdOutput)
	}
	return goTestOutputSuggestsHandlerWebCwd404Legacy(cmdOutput)
}

func formatHintBlock(block HTTPHintBlock, vars map[string]string) string {
	if block.Title == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("### ")
	b.WriteString(expandProfileTemplate(block.Title, vars))
	b.WriteString("\n")
	if block.Cause != "" {
		b.WriteString(expandProfileTemplate(block.Cause, vars))
		b.WriteString("\n")
	}
	for _, section := range [][]string{block.Fixes, block.FixesHandlersBead, block.FixesTestBead} {
		for _, line := range section {
			b.WriteString(expandProfileTemplate(line, vars))
			b.WriteString("\n")
		}
	}
	if block.ChdirLine != "" {
		b.WriteString(expandProfileTemplate(block.ChdirLine, vars))
		b.WriteString("\n")
	}
	if block.Footer != "" {
		b.WriteString(expandProfileTemplate(block.Footer, vars))
	}
	return strings.TrimSpace(b.String())
}

// FormatTraversalRedirectHint returns configured hint text (empty if disabled or generic).
func (p HTTPImplementationProfile) FormatTraversalRedirectHint(beadPath string, v WorkflowValidation) string {
	if !p.Enabled {
		return ""
	}
	return formatHintBlock(p.Hints.TraversalRedirect, p.vars(beadPath, v))
}

func (p HTTPImplementationProfile) FormatHandlerTestCwdHint(activeBeadPath, cmdOutput string, v WorkflowValidation) string {
	if !p.GoTestOutputSuggestsHandlerWebCwd404(cmdOutput) {
		return ""
	}
	vars := p.vars(handlerTestPathForHints(activeBeadPath, cmdOutput, v), v)
	block := p.Hints.TestCwd
	if block.Title == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(formatHintBlock(HTTPHintBlock{Title: block.Title, Cause: block.Cause}, vars))
	b.WriteString("\n")
	if IsHTTPHandlerImplementPath(activeBeadPath) {
		for _, line := range block.FixesHandlersBead {
			b.WriteString(expandProfileTemplate(line, vars))
			b.WriteString("\n")
		}
	} else {
		for _, line := range block.FixesTestBead {
			b.WriteString(expandProfileTemplate(line, vars))
			b.WriteString("\n")
		}
	}
	if block.ChdirLine != "" {
		b.WriteString(expandProfileTemplate(block.ChdirLine, vars))
		b.WriteString("\n")
	}
	if block.Footer != "" {
		b.WriteString(expandProfileTemplate(block.Footer, vars))
	}
	return strings.TrimSpace(b.String())
}

func (p HTTPImplementationProfile) FormatHandlerRoot404ServeWebHint(activeBeadPath, cmdOutput string, v WorkflowValidation) string {
	if !p.GoTestOutputSuggestsHandlerWebCwd404(cmdOutput) || !IsHTTPHandlerImplementPath(activeBeadPath) {
		return ""
	}
	return formatHintBlock(p.Hints.Root404ServeHelper, p.vars(activeBeadPath, v))
}

// FormatHTTPImplementGuidance renders architecture + profile implement-context lines.
func (p HTTPImplementationProfile) FormatHTTPImplementGuidance(beadPath string, v WorkflowValidation) string {
	if !IsHTTPRoutingGuidanceBead(beadPath) {
		return ""
	}
	vars := p.vars(beadPath, v)
	var lines []string
	lines = append(lines, p.expandLines(p.ImplementGuidance.Common, vars)...)
	if IsHTTPHandlerTestPath(beadPath) {
		lines = append(lines, p.expandLines(p.ImplementGuidance.TestBead, vars)...)
	} else if IsHTTPHandlerImplementPath(beadPath) {
		lines = append(lines, p.expandLines(p.ImplementGuidance.HandlerBead, vars)...)
	}
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### HTTP routing / tests (profile: ")
	b.WriteString(p.ID)
	b.WriteString(")\n")
	for _, line := range lines {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// HandlerTestMissingModuleChdirIssues uses profile enablement; message from rig JSON or default.
func (p HTTPImplementationProfile) HandlerTestMissingModuleChdirIssues(relPath, content string, v WorkflowValidation) []string {
	if !p.Enabled || !IsHTTPHandlerTestPath(relPath) {
		return nil
	}
	if strings.Contains(content, "t.Chdir") || strings.Contains(content, "os.Chdir") {
		if handlerTestUsesModuleRootChdir(content) {
			return nil
		}
		return []string{
			"do not os.Chdir into t.TempDir() or other fake layouts — use module-root cwd: os.Chdir(" + ChdirExprToModuleRootFromTest(relPath, v.LayoutRoot) + ") at Test* start",
		}
	}
	if !strings.Contains(content, "RegisterHandlers") && !strings.Contains(content, `httptest.NewRequest`) {
		return nil
	}
	chdir := ChdirExprToModuleRootFromTest(relPath, v.LayoutRoot)
	return []string{
		fmt.Sprintf("add module-root cwd before httptest (go test cwd is the package dir): os.Chdir(%s) at Test* start", chdir),
	}
}

func handlerTestUsesModuleRootChdir(content string) bool {
	if strings.Contains(content, "t.TempDir()") && strings.Contains(content, "Chdir") {
		return false
	}
	return strings.Contains(content, `filepath.Join(".."`) || strings.Contains(content, "filepath.Join(..")
}

// InstallDefaultHTTPProfiles copies embedded defaults into town orchestrator/http-profiles/.
func InstallDefaultHTTPProfiles(townRoot string) error {
	if townRoot == "" {
		return nil
	}
	destDir := filepath.Join(townRoot, "orchestrator", httpProfilesTownSubdir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	entries, err := embeddedHTTPProfiles.ReadDir("httpprofiles/defaults")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := embeddedHTTPProfiles.ReadFile("httpprofiles/defaults/" + e.Name())
		if err != nil {
			return err
		}
		dest := filepath.Join(destDir, e.Name())
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// WriteExampleHTTPImplementationRigConfig is deprecated; use EnsureHTTPImplementationRigConfig.
func WriteExampleHTTPImplementationRigConfig(townRoot, rig, profileID string) error {
	v := WorkflowValidation{QAVerifyCommand: "go test ./..."}
	if profileID != "" && profileID != defaultHTTPProfileID {
		path := HTTPImplementationRigConfigPath(townRoot, rig)
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		cfg := HTTPImplementationRigConfig{Profile: profileID}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(cfg); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
			return err
		}
		invalidateHTTPProfileCache()
		return nil
	}
	_, err := EnsureHTTPImplementationRigConfig(townRoot, rig, v)
	return err
}

// InvalidateHTTPProfileCacheForTest clears the profile cache (tests only).
func InvalidateHTTPProfileCacheForTest() {
	invalidateHTTPProfileCache()
}
