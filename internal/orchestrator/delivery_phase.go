package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DeliveryPhase is one delivery wave within a large rig spec (subset of required_files).
type DeliveryPhase struct {
	ID              string   `yaml:"id" json:"id"`
	Title           string   `yaml:"title,omitempty" json:"title,omitempty"`
	RequiredFiles   []string `yaml:"required_files" json:"required_files"`
	QAVerifyCommand string   `yaml:"qa_verify_command,omitempty" json:"qa_verify_command,omitempty"`
	DependsOn       []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	SpecFocus       string   `yaml:"spec_focus,omitempty" json:"spec_focus,omitempty"`
}

// HasPhasedDelivery reports whether the profile defines delivery phases.
func (v WorkflowValidation) HasPhasedDelivery() bool {
	return len(v.DeliveryPhases) > 0
}

// ActivePhaseID returns the current phase id (trimmed).
func (v WorkflowValidation) ActivePhaseID() string {
	return strings.TrimSpace(v.ActivePhaseIDField)
}

// ActivePhase returns the delivery phase matching ActivePhaseID, or the first phase when unset.
func (v WorkflowValidation) ActivePhase() (DeliveryPhase, bool) {
	if !v.HasPhasedDelivery() {
		return DeliveryPhase{}, false
	}
	want := v.ActivePhaseID()
	if want != "" {
		for _, p := range v.DeliveryPhases {
			if strings.TrimSpace(p.ID) == want {
				return p, true
			}
		}
		return DeliveryPhase{}, false
	}
	p := v.DeliveryPhases[0]
	return p, true
}

// CompletedPhaseIDs returns the completed phase id list.
func (v WorkflowValidation) CompletedPhaseIDs() []string {
	return v.CompletedPhaseIDsField
}

// IsPhaseCompleted reports whether the given phase id was previously completed.
func (v WorkflowValidation) IsPhaseCompleted(id string) bool {
	id = strings.TrimSpace(id)
	for _, c := range v.CompletedPhaseIDsField {
		if c == id {
			return true
		}
	}
	return false
}

// ResolveActivePhaseFromDisk returns the first phase whose required files are not all
// present on disk. When no files exist (fresh start) this returns phase 0.
func ResolveActivePhaseFromDisk(rigDir string, v WorkflowValidation) string {
	if len(v.DeliveryPhases) == 0 {
		return ""
	}
	for _, p := range v.DeliveryPhases {
		for _, f := range normalizePathList(p.RequiredFiles) {
			if _, err := os.Stat(filepath.Join(rigDir, f)); os.IsNotExist(err) {
				return strings.TrimSpace(p.ID)
			}
		}
	}
	return strings.TrimSpace(v.DeliveryPhases[len(v.DeliveryPhases)-1].ID)
}

// ActiveRequiredFiles returns paths in scope for the current delivery phase.
// When no phases are defined, returns RequiredFiles.
func (v WorkflowValidation) ActiveRequiredFiles() []string {
	if p, ok := v.ActivePhase(); ok && len(p.RequiredFiles) > 0 {
		return normalizePathList(p.RequiredFiles)
	}
	return normalizePathList(v.RequiredFiles)
}

// ActivePhaseQAVerifyCommand returns phase-specific QA command when set, else profile default.
func (v WorkflowValidation) ActivePhaseQAVerifyCommand() string {
	if p, ok := v.ActivePhase(); ok {
		if q := strings.TrimSpace(p.QAVerifyCommand); q != "" {
			return q
		}
	}
	return strings.TrimSpace(v.QAVerifyCommand)
}

// FindDeliveryPhaseForFile returns the index of the delivery phase that contains the given file,
// or -1 if not found in any phase. Works with unphased validation too.
func (v WorkflowValidation) FindDeliveryPhaseForFile(filePath string) int {
	filePath = filepath.ToSlash(strings.TrimSpace(filePath))
	if filePath == "" || len(v.DeliveryPhases) == 0 {
		return -1
	}
	for i, p := range v.DeliveryPhases {
		for _, f := range p.RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f == filePath {
				return i
			}
		}
	}
	return -1
}

// FileInCompletedPhase reports whether the file belongs to a phase that has already completed
// (i.e., its phase index is strictly less than the active phase index).
func (v WorkflowValidation) FileInCompletedPhase(filePath string) bool {
	idx := v.FindDeliveryPhaseForFile(filePath)
	if idx < 0 {
		return false
	}
	activeIdx := v.ActivePhaseIndex()
	if activeIdx < 0 {
		return false
	}
	return idx < activeIdx
}

// IsFinalDeliveryPhase reports whether the currently active delivery phase is the last one.
func (v WorkflowValidation) IsFinalDeliveryPhase() bool {
	if !v.HasPhasedDelivery() {
		return false
	}
	activeIdx := v.ActivePhaseIndex()
	if activeIdx < 0 {
		return false
	}
	return activeIdx == len(v.DeliveryPhases)-1
}

// ActivePhaseIndex returns the index of the active delivery phase, or -1 if not found.
func (v WorkflowValidation) ActivePhaseIndex() int {
	if !v.HasPhasedDelivery() {
		return -1
	}
	want := v.ActivePhaseID()
	for i, p := range v.DeliveryPhases {
		if strings.TrimSpace(p.ID) == want {
			return i
		}
	}
	return -1
}

// UnionRequiredFiles returns all paths across delivery phases (deduped), or RequiredFiles when unphased.
// Safe to call on ForActivePhase-scoped validation: phases still hold the full union.
func (v WorkflowValidation) UnionRequiredFiles() []string {
	if len(v.DeliveryPhases) == 0 {
		return normalizePathList(v.RequiredFiles)
	}
	seen := make(map[string]bool)
	var union []string
	add := func(paths []string) {
		for _, f := range normalizePathList(paths) {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			union = append(union, f)
		}
	}
	for _, p := range v.DeliveryPhases {
		add(p.RequiredFiles)
	}
	add(v.RequiredFiles)
	return union
}

// RequiredFilesForSmokeScope returns paths that gate runtime smoke and web-asset checks for the
// current workflow step. Phased rigs use the active delivery phase only — not later phases.
func (v WorkflowValidation) RequiredFilesForSmokeScope() []string {
	if v.HasPhasedDelivery() {
		return v.ActiveRequiredFiles()
	}
	seen := make(map[string]bool)
	var out []string
	for _, f := range append(v.UnionRequiredFiles(), v.RequiredFiles...) {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// PhasedActiveAndPastRequiredFiles returns required files from the active phase and all earlier phases,
// but NOT from future phases. Used for stub/artifact validation during phased delivery so future-phase
// files that haven't been created yet don't block the current phase from completing.
func (v WorkflowValidation) PhasedActiveAndPastRequiredFiles() []string {
	if !v.HasPhasedDelivery() {
		return normalizePathList(v.RequiredFiles)
	}
	activeID := v.ActivePhaseID()
	seen := make(map[string]bool)
	var out []string
	for _, p := range v.DeliveryPhases {
		add := normalizePathList(p.RequiredFiles)
		for _, f := range add {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
		if strings.TrimSpace(p.ID) == activeID {
			break // stop at active phase; future phases' files don't exist yet
		}
	}
	return out
}

// ForActivePhase returns a copy of v with RequiredFiles and QAVerifyCommand scoped to the active phase.
func (v WorkflowValidation) ForActivePhase() WorkflowValidation {
	if !v.HasPhasedDelivery() {
		return v
	}
	out := v
	if files := v.ActiveRequiredFiles(); len(files) > 0 {
		out.RequiredFiles = append([]string(nil), files...)
	}
	out.QAVerifyCommand = v.ActivePhaseQAVerifyCommand()
	if WorkflowUsesGo(out) && PhaseIsGoModOnly(out) {
		out.QAVerifyCommand = GoModPhaseQAVerifyCommand(out)
	}
	return out
}

// PhaseIsGoModOnly reports active required_files are only go.mod (no .go, web, or other artifacts).
func PhaseIsGoModOnly(v WorkflowValidation) bool {
	if len(v.RequiredFiles) == 0 {
		return false
	}
	hasGoMod := false
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		if strings.HasSuffix(f, "/go.mod") || f == "go.mod" {
			hasGoMod = true
			continue
		}
		return false
	}
	return hasGoMod
}

// IsDockerPackagingPath reports container packaging files (Dockerfile, compose, .dockerignore).
// These belong in the final delivery phase after application source exists.
func IsDockerPackagingPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	if base == "dockerfile" || base == "containerfile" || base == ".dockerignore" {
		return true
	}
	return strings.Contains(lower, "docker-compose")
}

func sortDockerPackagingPaths(paths []string) []string {
	score := func(p string) int {
		lower := strings.ToLower(p)
		switch {
		case strings.HasSuffix(lower, "dockerfile") || strings.HasSuffix(lower, "containerfile"):
			return 0
		case strings.Contains(lower, "docker-compose") && strings.Contains(lower, ".test."):
			return 2
		case strings.Contains(lower, "docker-compose"):
			return 1
		default:
			return 3
		}
	}
	out := append([]string(nil), paths...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if score(out[j]) < score(out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// qaVerifyCommandReferencesDocker reports whether a QA verify command needs Docker
// packaging files (docker-compose, docker build, Dockerfile) to run.
func qaVerifyCommandReferencesDocker(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "docker-compose") ||
		strings.Contains(lower, "docker compose") ||
		strings.Contains(lower, "docker build") ||
		strings.Contains(lower, "dockerfile")
}

// moveDockerPathsToFinalDeliveryPhase pulls Dockerfile/compose paths out of early phases
// and appends them to the last phase so polecats implement real code before container wiring.
// If an earlier phase's QA verify command referenced those Docker files, the command is
// moved to the last phase as well so each phase only runs a command it has files for.
func moveDockerPathsToFinalDeliveryPhase(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	seen := make(map[string]bool)
	var dockerPaths []string
	movedCmd := ""
	for i := range v.DeliveryPhases {
		var kept []string
		movedDockerFromPhase := false
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			if IsDockerPackagingPath(f) {
				if !seen[f] {
					seen[f] = true
					dockerPaths = append(dockerPaths, f)
				}
				movedDockerFromPhase = true
				continue
			}
			kept = append(kept, f)
		}
		v.DeliveryPhases[i].RequiredFiles = kept
		if movedDockerFromPhase && movedCmd == "" && qaVerifyCommandReferencesDocker(v.DeliveryPhases[i].QAVerifyCommand) {
			movedCmd = v.DeliveryPhases[i].QAVerifyCommand
			v.DeliveryPhases[i].QAVerifyCommand = ""
		}
	}
	if len(dockerPaths) == 0 {
		return v
	}
	dockerPaths = sortDockerPackagingPaths(dockerPaths)

	last := len(v.DeliveryPhases) - 1
	v.DeliveryPhases[last].RequiredFiles = append(
		normalizePathList(v.DeliveryPhases[last].RequiredFiles),
		dockerPaths...,
	)
	if movedCmd != "" {
		v.DeliveryPhases[last].QAVerifyCommand = movedCmd
	}

	active := v.ActivePhaseID()
	var phases []DeliveryPhase
	for _, p := range v.DeliveryPhases {
		if len(p.RequiredFiles) == 0 {
			if active != "" && strings.TrimSpace(p.ID) == active {
				v.ActivePhaseIDField = ""
			}
			continue
		}
		phases = append(phases, p)
	}
	if len(phases) == 0 {
		return v
	}
	v.DeliveryPhases = phases
	return v
}

// inferDefaultDeliveryPhases splits flat Go+web required_files into delivery waves when the
// profile omitted delivery_phases (common for Link Shelf–scale specs).
func inferDefaultDeliveryPhases(v WorkflowValidation) []DeliveryPhase {
	files := normalizePathList(v.RequiredFiles)
	if len(files) < 5 || len(files) > 25 {
		return nil
	}
	if WorkflowUsesGo(v) {
		return inferGoDeliveryPhases(files, v)
	}
	if WorkflowUsesPython(v) {
		return inferPythonDeliveryPhases(files, v)
	}
	return nil
}

func inferGoDeliveryPhases(files []string, v WorkflowValidation) []DeliveryPhase {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	var goMod, store, api, server, webStatic, webHTML []string
	for _, f := range files {
		lower := strings.ToLower(f)
		switch {
		case strings.HasSuffix(lower, "go.mod"):
			goMod = append(goMod, f)
		case strings.Contains(lower, "/internal/store/"):
			store = append(store, f)
		case strings.Contains(lower, "/internal/api/"):
			api = append(api, f)
		case strings.Contains(lower, "/cmd/"):
			server = append(server, f)
		case strings.Contains(lower, "/web/") && strings.HasSuffix(lower, "index.html"):
			webHTML = append(webHTML, f)
		case strings.Contains(lower, "/web/"):
			webStatic = append(webStatic, f)
		}
	}
	if len(goMod) == 0 || len(store) == 0 {
		return nil
	}
	if len(webStatic) == 0 && len(webHTML) == 0 {
		return nil
	}
	modQA := goModVerifyCommand(layout)
	var phases []DeliveryPhase
	phases = append(phases, DeliveryPhase{
		ID: "go-module", Title: "Go module", RequiredFiles: goMod, QAVerifyCommand: modQA,
	})
	if len(store) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "store-layer", Title: "Store layer",
			RequiredFiles:   OrderRequiredFilesForImplementation(store),
			QAVerifyCommand: goPackageVerifyCommand(layout, "./internal/store/..."),
		})
	}
	if len(api) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "api-handlers", Title: "HTTP handlers", RequiredFiles: api,
			QAVerifyCommand: goPackageVerifyCommand(layout, "./internal/api/..."),
		})
	}
	if len(server) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "server-main", Title: "Server entrypoint", RequiredFiles: server,
			QAVerifyCommand: goPackageVerifyCommand(layout, "./cmd/server/..."),
		})
	}
	if len(webStatic) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "web-static", Title: "Web static assets",
			RequiredFiles: OrderRequiredFilesForImplementation(webStatic),
		})
	}
	if len(webHTML) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "web-shell", Title: "Web HTML shell", RequiredFiles: webHTML,
		})
	}
	if len(phases) < 2 {
		return nil
	}
	return phases
}

func inferPythonDeliveryPhases(files []string, v WorkflowValidation) []DeliveryPhase {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	var requirements, backendSrc, backendTests, frontendJS, frontendHTML []string
	for _, f := range files {
		lower := strings.ToLower(f)
		switch {
		case strings.HasSuffix(lower, "requirements.txt"):
			requirements = append(requirements, f)
		case strings.Contains(lower, "/test") || strings.HasPrefix(filepath.Base(lower), "test_"):
			backendTests = append(backendTests, f)
		case strings.HasSuffix(lower, ".py"):
			backendSrc = append(backendSrc, f)
		case strings.Contains(lower, "/frontend/") || strings.Contains(lower, "/game/"):
			if strings.HasSuffix(lower, ".js") {
				frontendJS = append(frontendJS, f)
			} else if strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".css") {
				frontendHTML = append(frontendHTML, f)
			}
		case strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".css"):
			frontendHTML = append(frontendHTML, f)
		case strings.HasSuffix(lower, ".js"):
			frontendJS = append(frontendJS, f)
		}
	}
	requirements = append(requirements, backendSrc...)
	if len(requirements)+len(backendTests)+len(frontendJS) == 0 {
		return nil
	}
	fullQA := strings.TrimSpace(v.QAVerifyCommand)
	var phases []DeliveryPhase
	if len(requirements) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "backend-src", Title: "Backend source",
			RequiredFiles:   OrderRequiredFilesForImplementation(requirements),
			QAVerifyCommand: pythonVerifyCommand(layout, ".", fullQA),
		})
	}
	if len(backendTests) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "backend-tests", Title: "Backend tests",
			RequiredFiles:   OrderRequiredFilesForImplementation(backendTests),
			QAVerifyCommand: pythonVerifyCommand(layout, ".", fullQA),
		})
	}
	if len(frontendJS) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "frontend-js", Title: "Frontend JavaScript",
			RequiredFiles: OrderRequiredFilesForImplementation(frontendJS),
		})
	}
	if len(frontendHTML) > 0 {
		phases = append(phases, DeliveryPhase{
			ID: "frontend-html", Title: "Frontend HTML/CSS",
			RequiredFiles: OrderRequiredFilesForImplementation(frontendHTML),
		})
	}
	if len(phases) < 2 {
		return nil
	}
	return phases
}

func pythonVerifyCommand(layout, relDir, defaultQA string) string {
	if q := strings.TrimSpace(defaultQA); q != "" {
		return q
	}
	if layout == "" || layout == "." {
		return "pytest " + relDir
	}
	return "cd " + layout + " && pytest " + relDir
}

func goModVerifyCommand(layout string) string {
	if layout == "" || layout == "." {
		return "go mod download"
	}
	return "cd " + layout + " && go mod download"
}

func goPackageVerifyCommand(layout, pkg string) string {
	if layout == "" || layout == "." {
		return "go test " + pkg
	}
	return "cd " + layout + " && go test " + pkg
}

// maxFilesPerPhase caps how many files a single delivery phase can hold.
// With ~100 max LLM calls and ~10 calls needed per file, no phase should exceed 10 files.
const maxFilesPerPhase = 10

// collapseSplitDeliveryPhases merges phases that were previously split (e.g.
// e2e-and-deployment-1, e2e-and-deployment-2 back into e2e-and-deployment).
// This prevents recursive splitting when ClampProfileValidation runs on save.
func collapseSplitDeliveryPhases(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	baseID := func(id string) string {
		id = strings.TrimSpace(id)
		for {
			idx := strings.LastIndex(id, "-")
			if idx < 0 {
				return id
			}
			suffix := id[idx+1:]
			if suffix == "" {
				return id
			}
			numeric := true
			for _, r := range suffix {
				if r < '0' || r > '9' {
					numeric = false
					break
				}
			}
			if !numeric {
				return id
			}
			id = id[:idx]
		}
	}
	groups := map[string][]DeliveryPhase{}
	order := []string{}
	for _, p := range v.DeliveryPhases {
		b := baseID(p.ID)
		if _, ok := groups[b]; !ok {
			order = append(order, b)
		}
		groups[b] = append(groups[b], p)
	}
	var collapsed []DeliveryPhase
	for _, b := range order {
		parts := groups[b]
		if len(parts) == 1 {
			collapsed = append(collapsed, parts[0])
			continue
		}
		baseTitle := strings.TrimSpace(parts[0].Title)
		for {
			idx := strings.LastIndex(baseTitle, " (part ")
			if idx <= 0 {
				break
			}
			baseTitle = strings.TrimSpace(baseTitle[:idx])
		}
		merged := DeliveryPhase{
			ID:              b,
			Title:           baseTitle,
			RequiredFiles:   []string{},
			QAVerifyCommand: parts[0].QAVerifyCommand,
			DependsOn:       parts[0].DependsOn,
			SpecFocus:       parts[0].SpecFocus,
		}
		seen := map[string]bool{}
		for _, p := range parts {
			for _, f := range p.RequiredFiles {
				f = filepath.ToSlash(strings.TrimSpace(f))
				if f == "" || seen[f] {
					continue
				}
				seen[f] = true
				merged.RequiredFiles = append(merged.RequiredFiles, f)
			}
		}
		collapsed = append(collapsed, merged)
	}
	v.DeliveryPhases = collapsed
	return v
}

// splitOverlargePhases splits any delivery phase with more than maxFilesPerPhase files
// into multiple sequential phases, each capped at maxFilesPerPhase files.
func splitOverlargePhases(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	var split []DeliveryPhase
	for _, p := range v.DeliveryPhases {
		if len(p.RequiredFiles) <= maxFilesPerPhase {
			split = append(split, p)
			continue
		}
		// Skip phases already split (IDs ending with a numeric suffix like "-1", "-3-2").
		if id := strings.TrimSpace(p.ID); len(id) > 0 {
			last := id[len(id)-1]
			if last >= '0' && last <= '9' {
				split = append(split, p)
				continue
			}
		}
		chunks := chunkStrings(p.RequiredFiles, maxFilesPerPhase)
		for i, chunk := range chunks {
			partID := fmt.Sprintf("%s-%d", p.ID, i+1)
			partTitle := p.Title
			if partTitle == "" {
				partTitle = p.ID
			}
			partTitle = fmt.Sprintf("%s (part %d/%d)", partTitle, i+1, len(chunks))
			dp := DeliveryPhase{
				ID:              partID,
				Title:           partTitle,
				RequiredFiles:   chunk,
				QAVerifyCommand: p.QAVerifyCommand,
				DependsOn:       nil, // recalculated below
				SpecFocus:       p.SpecFocus,
			}
			if i == 0 {
				dp.DependsOn = p.DependsOn
			} else {
				dp.DependsOn = []string{fmt.Sprintf("%s-%d", p.ID, i)}
			}
			split = append(split, dp)
		}
	}
	v.DeliveryPhases = split
	return v
}

// chunkStrings splits a slice into sub-slices of at most chunkSize elements.
func chunkStrings(s []string, chunkSize int) [][]string {
	if len(s) == 0 {
		return nil
	}
	var chunks [][]string
	for i := 0; i < len(s); i += chunkSize {
		end := i + chunkSize
		if end > len(s) {
			end = len(s)
		}
		chunks = append(chunks, s[i:end])
	}
	return chunks
}

// pairPhaseTests removes test files from phases where their source file
// lives in a different phase, keeping source+test pairs together for
// implementation bead creation without bloating the RequiredFiles list.
func pairPhaseTests(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	// Build a map of test file → phase index for tests whose source is in another phase
	testToSourcePhase := make(map[string]int) // test path → phase index containing its source
	for i := range v.DeliveryPhases {
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			test := CorrelatedTestPathForSource(f, v)
			if test == "" {
				continue
			}
			// Check if this test exists in a different phase
			for j := range v.DeliveryPhases {
				if j == i {
					continue
				}
				for _, rf := range v.DeliveryPhases[j].RequiredFiles {
					if pathMatchesRequired(test, []string{rf}) {
						testToSourcePhase[test] = i
					}
				}
			}
		}
	}
	if len(testToSourcePhase) == 0 {
		return v
	}
	// Remove tests from phases where their source isn't present
	for i := range v.DeliveryPhases {
		var kept []string
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			if !IsTestImplementPath(f) {
				kept = append(kept, f)
				continue
			}
			sourcePhase, moved := testToSourcePhase[f]
			if moved && sourcePhase != i {
				// Test's source is in another phase — drop from here
				continue
			}
			kept = append(kept, f)
		}
		v.DeliveryPhases[i].RequiredFiles = kept
	}
	// Also strip moved test files from the union RequiredFiles so QA scope doesn't include them
	movedTests := make(map[string]bool, len(testToSourcePhase))
	for test := range testToSourcePhase {
		movedTests[test] = true
	}
	var keptUnion []string
	for _, f := range v.RequiredFiles {
		if IsTestImplementPath(f) && movedTests[f] {
			continue
		}
		keptUnion = append(keptUnion, f)
	}
	if len(keptUnion) < len(v.RequiredFiles) {
		v.RequiredFiles = keptUnion
	}
	return v
}

// pairPhaseInfraFiles ensures each delivery phase includes common infrastructure files
// (package.json, tsconfig.json) when it contains code files that depend on them.
// These files are recognized as project-setup-owned via IsProjectSetupArtifactPath,
// so project_setup creates them rather than the implementer polecat.
func pairPhaseInfraFiles(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	unionSet := make(map[string]bool, len(v.RequiredFiles))
	for _, f := range v.RequiredFiles {
		unionSet[filepath.ToSlash(strings.TrimSpace(f))] = true
	}
	for i := range v.DeliveryPhases {
		phaseSet := make(map[string]bool, len(v.DeliveryPhases[i].RequiredFiles))
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			phaseSet[filepath.ToSlash(strings.TrimSpace(f))] = true
		}
		var pending []string
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			if !WorkflowUsesNodeJS(v) {
				continue
			}
			ext := strings.ToLower(filepath.Ext(f))
			base := strings.ToLower(filepath.Base(f))
			if ext != ".tsx" && ext != ".ts" && ext != ".js" && ext != ".jsx" {
				continue
			}
			// Don't generate a parent package.json/tsconfig.json for the infra files themselves.
			if base == "package.json" || base == "tsconfig.json" {
				continue
			}
			dir := filepath.ToSlash(filepath.Dir(f))
			parent := filepath.ToSlash(filepath.Dir(dir))
			// Only add parent package.json/tsconfig.json; never generate a manifest
			// at the rig root (e.g. for files directly under test/ or frontend/). Those
			// files are covered by their own dir-level manifests when present.
			if parent == "" || parent == "." {
				continue
			}
			candidates := []string{
				filepath.ToSlash(filepath.Join(dir, "..", "package.json")),
				filepath.ToSlash(filepath.Join(dir, "..", "tsconfig.json")),
			}
			for _, c := range candidates {
				if phaseSet[c] {
					continue
				}
				pending = append(pending, c)
				phaseSet[c] = true
			}
		}
		if len(pending) > 0 {
			v.DeliveryPhases[i].RequiredFiles = append(pending, v.DeliveryPhases[i].RequiredFiles...)
			for _, p := range pending {
				if !unionSet[p] {
					v.RequiredFiles = append(v.RequiredFiles, p)
					unionSet[p] = true
				}
			}
		}
	}
	return v
}

// FinalizeDeliveryPhases unions phase file lists into RequiredFiles, sets default active phase, normalizes paths.
func FinalizeDeliveryPhases(v WorkflowValidation) WorkflowValidation {
	v = collapseSplitDeliveryPhases(v)
	v = pairPhaseInfraFiles(v)
	v = splitOverlargePhases(v)
	v = pairPhaseInfraFiles(v)
	v = pairPhaseTests(v)
	if len(v.DeliveryPhases) == 0 {
		if inferred := inferDefaultDeliveryPhases(v); len(inferred) > 0 {
			v.DeliveryPhases = inferred
		}
	}
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	v = moveDockerPathsToFinalDeliveryPhase(v)
	v = reorderDeliveryPhasesWebAfterBackend(v)
	seen := make(map[string]bool)
	var union []string
	add := func(paths []string) {
		for _, f := range normalizePathList(paths) {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			union = append(union, f)
		}
	}
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = normalizePathList(v.DeliveryPhases[i].RequiredFiles)
		add(v.DeliveryPhases[i].RequiredFiles)
	}
	add(v.RequiredFiles)
	if len(union) > 0 {
		v.RequiredFiles = union
	}
	if v.ActivePhaseID() == "" && len(v.DeliveryPhases) > 0 {
		v.ActivePhaseIDField = strings.TrimSpace(v.DeliveryPhases[0].ID)
	}
	// If the active phase ID no longer matches (e.g. after splitting), map it to the
	// first sub-phase that starts with the old ID prefix.
	if v.ActivePhaseID() != "" && len(v.DeliveryPhases) > 0 {
		found := false
		for _, p := range v.DeliveryPhases {
			if strings.TrimSpace(p.ID) == v.ActivePhaseID() {
				found = true
				break
			}
		}
		if !found {
			prefix := v.ActivePhaseID()
			for _, p := range v.DeliveryPhases {
				if strings.HasPrefix(strings.TrimSpace(p.ID), prefix) {
					v.ActivePhaseIDField = strings.TrimSpace(p.ID)
					break
				}
			}
		}
	}
	return v
}

func normalizePathList(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// NormalizeDeliveryPhasesLayout prefixes phase required_files with layout_root like RequiredFiles.
func NormalizeDeliveryPhasesLayout(v WorkflowValidation) WorkflowValidation {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return v
	}
	for i := range v.DeliveryPhases {
		if len(v.DeliveryPhases[i].RequiredFiles) == 0 {
			continue
		}
		out := make([]string, 0, len(v.DeliveryPhases[i].RequiredFiles))
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f == "" {
				continue
			}
			if !strings.HasPrefix(f, layout+"/") && !strings.Contains(f, "..") {
				f = layout + "/" + strings.TrimPrefix(f, "/")
			}
			out = append(out, f)
		}
		v.DeliveryPhases[i].RequiredFiles = out
	}
	return v
}

// PhaseScopeNote returns planner/polecat guidance when delivery phases are configured.
func (v WorkflowValidation) PhaseScopeNote() string {
	if !v.HasPhasedDelivery() {
		return ""
	}
	activeID := v.ActivePhaseID()
	if p, ok := v.ActivePhase(); ok && activeID == "" {
		activeID = strings.TrimSpace(p.ID)
	}
	title := ""
	if p, ok := v.ActivePhase(); ok {
		title = strings.TrimSpace(p.Title)
	}
	line := "**Phased delivery:** active phase `" + activeID + "`"
	if title != "" {
		line += " (" + title + ")"
	}
	activeFiles := v.ActiveRequiredFiles()
	line += fmt.Sprintf(". Create implement beads **only** for the %d paths in `required_files` below", len(activeFiles))
	line += " — **not** every path in `architecture.md` (later phases get their own beads). "
	line += "`plan.md` only needs to cover this phase (" + v.PlanMinSizeHint() + "). "
	line += "Read architecture for context; do **not** `bd create` for backend/frontend/test paths until their phase is active. "
	line += "When QA reports `all_passed` for this phase, the orchestrator **automatically** advances to the next phase and restarts at planning."
	return line
}

// NextDeliveryPhaseID returns the phase id after the active one, or ("", false) on the last phase.
func (v WorkflowValidation) NextDeliveryPhaseID() (string, bool) {
	if len(v.DeliveryPhases) < 2 {
		return "", false
	}
	active := v.ActivePhaseID()
	idx := 0
	if active != "" {
		found := false
		for i, p := range v.DeliveryPhases {
			if strings.TrimSpace(p.ID) == active {
				idx = i
				found = true
				break
			}
		}
		if !found {
			return "", false
		}
	}
	if idx+1 >= len(v.DeliveryPhases) {
		return "", false
	}
	return strings.TrimSpace(v.DeliveryPhases[idx+1].ID), true
}

// TryAdvanceDeliveryPhaseAfterQA moves active_phase_id to the next phase and syncs planning beads/plan.md.
// Call when QA reports all_passed for the current phase. Returns redirected=true when the workflow should
// continue at planning instead of completed.
//
// Once SetRigActivePhase succeeds, redirected is always true — pruning and sync failures are non-fatal
// (logged as warnings in logLine) so that transient issues like Dolt being down don't cause the workflow
// to prematurely complete.
func TryAdvanceDeliveryPhaseAfterQA(townRoot, rig string) (redirected bool, fromID, toID, logLine string, err error) {
	if townRoot == "" || rig == "" {
		return false, "", "", "", nil
	}
	full, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		return false, "", "", "", err
	}
	if !ok || !full.HasPhasedDelivery() {
		return false, "", "", "", nil
	}
	fromID = full.ActivePhaseID()
	if fromID == "" {
		if p, ok := full.ActivePhase(); ok {
			fromID = strings.TrimSpace(p.ID)
		}
	}
	// Capture the phase list from the first load so spec-index re-runs
	// mid-session don't change which phase we advance from/to.
	phaseIDs := make([]string, len(full.DeliveryPhases))
	for i, p := range full.DeliveryPhases {
		phaseIDs[i] = strings.TrimSpace(p.ID)
	}
	nextID := ""
	// If the workflow was rewound from a later phase, jump directly back
	// to it instead of advancing sequentially through intermediates.
	if rp := strings.TrimSpace(full.RewoundFromPhaseIDField); rp != "" {
		for _, id := range phaseIDs {
			if id == rp {
				nextID = rp
				break
			}
		}
	}
	if nextID == "" {
		for i, id := range phaseIDs {
			if id == fromID && i+1 < len(phaseIDs) {
				nextID = phaseIDs[i+1]
				break
			}
		}
	}
	if nextID == "" {
		return false, fromID, "", "", nil
	}
	if err := SetRigActivePhase(townRoot, rig, nextID); err != nil {
		return false, fromID, "", "", err
	}
	// Phase advanced on disk — from here on, always return redirected=true.
	// Pruning and sync are best-effort; failures are warnings, not fatal.
	redirected = true
	_ = AddRigCompletedPhase(townRoot, rig, fromID) // best-effort; carry-on
	_ = ClearRigRewoundFromPhase(townRoot, rig)     // best-effort; carry-on
	full, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		return true, fromID, nextID, "", err
	}
	logLine = fmt.Sprintf("delivery phase advanced %s → %s", fromID, nextID)
	var parts []string
	pruned, pruneErr := PruneOpenImplementBeadsOutsideRequired(townRoot, rig, full)
	if pruneErr != nil {
		parts = append(parts, "prune warning: "+pruneErr.Error())
	} else if len(pruned) > 0 {
		parts = append(parts, "pruned prior-phase open beads: "+joinStrings(pruned, ", "))
	}
	syncLog, syncErr := SyncPlanningArtifacts(townRoot, rig, full, true)
	if syncErr != nil {
		parts = append(parts, "sync warning: "+syncErr.Error())
	} else if syncLog != "" {
		parts = append(parts, syncLog)
	}
	if len(parts) > 0 {
		logLine += " (" + strings.Join(parts, "; ") + ")"
	}
	return true, fromID, nextID, logLine, nil
}

// TryFastForwardDeliveryPhase advances through consecutive phases that have all beads closed,
// stopping at the first phase with open beads (or the last phase). Returns the final phase ID.
// This allows fast-forwarding through already-complete intermediate phases after QA passes.
func TryFastForwardDeliveryPhase(townRoot, rig string, v WorkflowValidation) (string, error) {
	if !v.HasPhasedDelivery() {
		return "", nil
	}
	activeID := v.ActivePhaseID()
	if activeID == "" {
		return "", nil
	}
	phaseIDs := make([]string, len(v.DeliveryPhases))
	for i, p := range v.DeliveryPhases {
		phaseIDs[i] = strings.TrimSpace(p.ID)
	}
	activeIdx := -1
	for i, id := range phaseIDs {
		if id == activeID {
			activeIdx = i
			break
		}
	}
	if activeIdx < 0 {
		return "", nil
	}
	// Scan forward from active phase
	for i := activeIdx; i < len(phaseIDs); i++ {
		phaseFiles := v.DeliveryPhases[i].RequiredFiles
		if len(phaseFiles) == 0 {
			continue
		}
		// Only skip phases that have been explicitly marked as completed.
		// Without this check, a newly-advanced phase with no beads yet would
		// be incorrectly fast-forwarded through.
		if !v.IsPhaseCompleted(phaseIDs[i]) {
			if i != activeIdx {
				return phaseIDs[i], SetRigActivePhase(townRoot, rig, phaseIDs[i])
			}
			return activeID, nil
		}
		// Phase was completed — verify no open beads remain (race guard).
		phaseValidation := v.ForActivePhase()
		phaseValidation.DeliveryPhases = []DeliveryPhase{v.DeliveryPhases[i]}
		open, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, phaseValidation)
		if err != nil {
			return "", err
		}
		if len(open) > 0 {
			// Phase was completed but has open beads (e.g. re-opened by rewind).
			// Fall out of fast-forward — this is where work is needed.
			if i != activeIdx {
				return phaseIDs[i], SetRigActivePhase(townRoot, rig, phaseIDs[i])
			}
			return activeID, nil
		}
	}
	lastID := phaseIDs[len(phaseIDs)-1]
	if lastID != activeID {
		return lastID, SetRigActivePhase(townRoot, rig, lastID)
	}
	return activeID, nil
}

// PhaseSummaryLines returns human-readable phase list for operator notices.
func (v WorkflowValidation) PhaseSummaryLines() []string {
	if !v.HasPhasedDelivery() {
		return nil
	}
	active := v.ActivePhaseID()
	var lines []string
	for _, p := range v.DeliveryPhases {
		id := strings.TrimSpace(p.ID)
		title := strings.TrimSpace(p.Title)
		label := id
		if title != "" {
			label = id + " — " + title
		}
		n := len(p.RequiredFiles)
		mark := ""
		if active != "" && id == active {
			mark = " (active)"
		} else if active == "" && len(lines) == 0 {
			mark = " (default active)"
		}
		lines = append(lines, fmt.Sprintf("%s: %d file(s)%s", label, n, mark))
	}
	return lines
}
