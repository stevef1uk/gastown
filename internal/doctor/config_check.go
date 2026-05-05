package doctor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/doltserver"
)

// SettingsCheck verifies each rig has a settings/ directory.
type SettingsCheck struct {
	FixableCheck
	missingSettings []string // Cached during Run for use in Fix
}

// NewSettingsCheck creates a new settings directory check.
func NewSettingsCheck() *SettingsCheck {
	return &SettingsCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "rig-settings",
				CheckDescription: "Check that rigs have settings/ directory",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks if all rigs have a settings/ directory.
func (c *SettingsCheck) Run(ctx *CheckContext) *CheckResult {
	rigs := c.findRigs(ctx.TownRoot)
	if len(rigs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rigs found",
		}
	}

	var missing []string
	var ok int

	for _, rig := range rigs {
		settingsPath := constants.RigSettingsPath(rig)
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			relPath, _ := filepath.Rel(ctx.TownRoot, rig)
			missing = append(missing, relPath)
		} else {
			ok++
		}
	}

	// Cache for Fix
	c.missingSettings = nil
	for _, rig := range rigs {
		settingsPath := constants.RigSettingsPath(rig)
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			c.missingSettings = append(c.missingSettings, settingsPath)
		}
	}

	if len(missing) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("All %d rig(s) have settings/ directory", ok),
		}
	}

	details := make([]string, len(missing))
	for i, m := range missing {
		details[i] = fmt.Sprintf("Missing: %s/settings/", m)
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d rig(s) missing settings/ directory", len(missing)),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to create missing directories",
	}
}

// Fix creates missing settings/ directories.
func (c *SettingsCheck) Fix(ctx *CheckContext) error {
	for _, path := range c.missingSettings {
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", path, err)
		}
	}
	return nil
}

// RuntimeGitignoreCheck verifies .runtime/ is gitignored at town and rig levels.
type RuntimeGitignoreCheck struct {
	BaseCheck
}

// NewRuntimeGitignoreCheck creates a new runtime gitignore check.
func NewRuntimeGitignoreCheck() *RuntimeGitignoreCheck {
	return &RuntimeGitignoreCheck{
		BaseCheck: BaseCheck{
			CheckName:        "runtime-gitignore",
			CheckDescription: "Check that .runtime/ directories are gitignored",
			CheckCategory:    CategoryConfig,
		},
	}
}

// Run checks if .runtime/ is properly gitignored.
func (c *RuntimeGitignoreCheck) Run(ctx *CheckContext) *CheckResult {
	var issues []string

	// Check town-level .gitignore
	townGitignore := filepath.Join(ctx.TownRoot, ".gitignore")
	if !c.containsPattern(townGitignore, ".runtime") {
		issues = append(issues, "Town .gitignore missing .runtime/ pattern")
	}

	// Check each rig's .gitignore (in their git worktrees)
	rigs := c.findRigs(ctx.TownRoot)
	for _, rig := range rigs {
		// Check crew members
		crewPath := filepath.Join(rig, "crew")
		if crewEntries, err := os.ReadDir(crewPath); err == nil {
			for _, crew := range crewEntries {
				if crew.IsDir() && !strings.HasPrefix(crew.Name(), ".") {
					crewGitignore := filepath.Join(crewPath, crew.Name(), ".gitignore")
					if !c.containsPattern(crewGitignore, ".runtime") {
						relPath, _ := filepath.Rel(ctx.TownRoot, filepath.Join(crewPath, crew.Name()))
						issues = append(issues, fmt.Sprintf("%s .gitignore missing .runtime/ pattern", relPath))
					}
				}
			}
		}
	}

	if len(issues) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: ".runtime/ properly gitignored",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d location(s) missing .runtime gitignore", len(issues)),
		Details: issues,
		FixHint: "Add '.runtime/' to .gitignore files",
	}
}

// containsPattern checks if a gitignore file contains a pattern.
func (c *RuntimeGitignoreCheck) containsPattern(gitignorePath, pattern string) bool {
	file, err := os.Open(gitignorePath)
	if err != nil {
		return false // File doesn't exist
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Check for pattern match (with or without trailing slash, with or without glob prefix)
		// Accept: .runtime, .runtime/, /.runtime, /.runtime/, **/.runtime, **/.runtime/
		if line == pattern || line == pattern+"/" ||
			line == "/"+pattern || line == "/"+pattern+"/" ||
			line == "**/"+pattern || line == "**/"+pattern+"/" {
			return true
		}
	}
	return false
}

// findRigs returns rig directories within the town.
func (c *RuntimeGitignoreCheck) findRigs(townRoot string) []string {
	return findAllRigs(townRoot)
}

// LegacyGastownCheck warns if old .gastown/ directories still exist.
type LegacyGastownCheck struct {
	FixableCheck
	legacyDirs []string // Cached during Run for use in Fix
}

// NewLegacyGastownCheck creates a new legacy gastown check.
func NewLegacyGastownCheck() *LegacyGastownCheck {
	return &LegacyGastownCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "legacy-gastown",
				CheckDescription: "Check for old .gastown/ directories that should be migrated",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks for legacy .gastown/ directories.
func (c *LegacyGastownCheck) Run(ctx *CheckContext) *CheckResult {
	var found []string

	// Check town-level .gastown/
	townGastown := filepath.Join(ctx.TownRoot, ".gastown")
	if info, err := os.Stat(townGastown); err == nil && info.IsDir() {
		found = append(found, ".gastown/ (town root)")
	}

	// Check each rig for .gastown/
	rigs := c.findRigs(ctx.TownRoot)
	for _, rig := range rigs {
		rigGastown := filepath.Join(rig, ".gastown")
		if info, err := os.Stat(rigGastown); err == nil && info.IsDir() {
			relPath, _ := filepath.Rel(ctx.TownRoot, rig)
			found = append(found, fmt.Sprintf("%s/.gastown/", relPath))
		}
	}

	// Cache for Fix
	c.legacyDirs = nil
	if info, err := os.Stat(townGastown); err == nil && info.IsDir() {
		c.legacyDirs = append(c.legacyDirs, townGastown)
	}
	for _, rig := range rigs {
		rigGastown := filepath.Join(rig, ".gastown")
		if info, err := os.Stat(rigGastown); err == nil && info.IsDir() {
			c.legacyDirs = append(c.legacyDirs, rigGastown)
		}
	}

	if len(found) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No legacy .gastown/ directories found",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d legacy .gastown/ directory(ies) found", len(found)),
		Details: found,
		FixHint: "Run 'gt doctor --fix' to remove after verifying migration is complete",
	}
}

// Fix removes legacy .gastown/ directories.
func (c *LegacyGastownCheck) Fix(ctx *CheckContext) error {
	for _, dir := range c.legacyDirs {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("failed to remove %s: %w", dir, err)
		}
	}
	return nil
}

// findRigs returns rig directories within the town.
func (c *LegacyGastownCheck) findRigs(townRoot string) []string {
	return findAllRigs(townRoot)
}

// findRigs returns rig directories within the town.
func (c *SettingsCheck) findRigs(townRoot string) []string {
	return findAllRigs(townRoot)
}

// SessionHookCheck verifies settings.json files use proper session_id passthrough.
// Valid options: session-start.sh wrapper OR 'gt prime --hook'.
// Without proper config, gt seance cannot discover sessions.
type SessionHookCheck struct {
	FixableCheck
	filesToFix []string // Cached during Run for use in Fix
}

// NewSessionHookCheck creates a new session hook check.
func NewSessionHookCheck() *SessionHookCheck {
	return &SessionHookCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "session-hooks",
				CheckDescription: "Check that settings.json hooks use session-start.sh or --hook flag",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks if all settings.json files use session-start.sh or --hook flag.
func (c *SessionHookCheck) Run(ctx *CheckContext) *CheckResult {
	var issues []string
	var checked int

	// Reset cache
	c.filesToFix = nil

	// Find all settings.json files in the town
	settingsFiles := c.findSettingsFiles(ctx.TownRoot)

	for _, settingsPath := range settingsFiles {
		relPath, _ := filepath.Rel(ctx.TownRoot, settingsPath)

		problems := c.checkSettingsFile(settingsPath)
		if len(problems) > 0 {
			for _, problem := range problems {
				issues = append(issues, fmt.Sprintf("%s: %s", relPath, problem))
			}
			// Cache file for Fix
			c.filesToFix = append(c.filesToFix, settingsPath)
		}
		checked++
	}

	if len(issues) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("All %d settings.json file(s) use proper session_id passthrough", checked),
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d hook issue(s) found across settings.json files", len(issues)),
		Details: issues,
		FixHint: "Run 'gt doctor --fix' to update hooks to use 'gt prime --hook'",
	}
}

// Fix updates settings.json files to use 'gt prime --hook' instead of bare 'gt prime'.
func (c *SessionHookCheck) Fix(ctx *CheckContext) error {
	for _, path := range c.filesToFix {
		if err := c.fixSettingsFile(path); err != nil {
			return fmt.Errorf("failed to fix %s: %w", path, err)
		}
	}
	return nil
}

// fixSettingsFile updates a single settings.json file.
func (c *SessionHookCheck) fixSettingsFile(path string) error {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Parse JSON to get structure
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	// Get hooks section
	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return nil // No hooks section, nothing to fix
	}

	modified := false

	// Fix SessionStart and PreCompact hooks
	for _, hookType := range []string{"SessionStart", "PreCompact"} {
		hookList, ok := hooks[hookType].([]interface{})
		if !ok {
			continue
		}

		for _, hookEntry := range hookList {
			entry, ok := hookEntry.(map[string]interface{})
			if !ok {
				continue
			}

			hooksList, ok := entry["hooks"].([]interface{})
			if !ok {
				continue
			}

			for _, hook := range hooksList {
				hookMap, ok := hook.(map[string]interface{})
				if !ok {
					continue
				}

				command, ok := hookMap["command"].(string)
				if !ok {
					continue
				}

				// Check if command has 'gt prime' without --hook
				if strings.Contains(command, "gt prime") && !containsFlag(command, "--hook") {
					// Replace 'gt prime' with 'gt prime --hook'
					newCommand := strings.Replace(command, "gt prime", "gt prime --hook", -1)
					hookMap["command"] = newCommand
					modified = true
				}
			}
		}
	}

	if !modified {
		return nil
	}

	// Marshal back to JSON with indentation, without HTML escaping
	// (json.MarshalIndent escapes & as \u0026 which is valid but less readable)
	buf := new(strings.Builder)
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(settings); err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	newData := []byte(buf.String())

	// Write back
	if err := os.WriteFile(path, newData, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// checkSettingsFile checks a single settings.json file for hook issues.
func (c *SessionHookCheck) checkSettingsFile(path string) []string {
	var problems []string

	data, err := os.ReadFile(path)
	if err != nil {
		return nil // Can't read file, skip
	}

	content := string(data)

	// Check for SessionStart hooks
	if strings.Contains(content, "SessionStart") {
		if !c.usesSessionStartScript(content, "SessionStart") {
			problems = append(problems, "SessionStart uses bare 'gt prime' - add --hook flag or use session-start.sh")
		}
	}

	// Check for PreCompact hooks
	if strings.Contains(content, "PreCompact") {
		if !c.usesSessionStartScript(content, "PreCompact") {
			problems = append(problems, "PreCompact uses bare 'gt prime' - add --hook flag or use session-start.sh")
		}
	}

	return problems
}

// usesSessionStartScript checks if the hook configuration handles session_id properly.
// Valid: session-start.sh wrapper OR 'gt prime --hook'. Returns true if properly configured.
func (c *SessionHookCheck) usesSessionStartScript(content, hookType string) bool {
	// Find the hook section - look for the hook type followed by its configuration
	// This is a simple heuristic - we look for "gt prime" without session-start.sh

	// Split around the hook type to find its section
	parts := strings.SplitN(content, `"`+hookType+`"`, 2)
	if len(parts) < 2 {
		return true // Hook type not found, nothing to check
	}

	// Get the section after the hook type declaration (until next top-level key)
	section := parts[1]

	// Find the end of this hook section (next top-level key at same depth)
	// Simple approach: look until we find another "Session" or "User" or end of hooks
	endMarkers := []string{`"SessionStart"`, `"PreCompact"`, `"UserPromptSubmit"`, `"Stop"`, `"Notification"`}
	sectionEnd := len(section)
	for _, marker := range endMarkers {
		if marker == `"`+hookType+`"` {
			continue // Skip the one we're looking for
		}
		if idx := strings.Index(section, marker); idx > 0 && idx < sectionEnd {
			sectionEnd = idx
		}
	}
	section = section[:sectionEnd]

	// Check if this section contains session-start.sh
	if strings.Contains(section, "session-start.sh") {
		return true // Uses the wrapper script
	}

	// Check if it uses 'gt prime --hook' which handles session_id via stdin
	if strings.Contains(section, "gt prime") {
		// gt prime --hook is valid - it reads session_id from stdin JSON
		// Must match --hook as complete flag, not substring (e.g., --hookup)
		if containsFlag(section, "--hook") {
			return true
		}
		// Bare 'gt prime' without --hook doesn't get session_id
		return false
	}

	// No gt prime or session-start.sh found - might be a different hook configuration
	return true
}

// findSettingsFiles finds all settings.json files in the town.
// Settings are installed in gastown-managed parent directories and passed via --settings flag.
func (c *SessionHookCheck) findSettingsFiles(townRoot string) []string {
	var files []string

	// Town-level agents: mayor and deacon (settings in their own dir)
	mayorSettings := filepath.Join(townRoot, "mayor", ".claude", "settings.json")
	if _, err := os.Stat(mayorSettings); err == nil {
		files = append(files, mayorSettings)
	}

	deaconSettings := filepath.Join(townRoot, "deacon", ".claude", "settings.json")
	if _, err := os.Stat(deaconSettings); err == nil {
		files = append(files, deaconSettings)
	}

	// Find all rigs
	rigs := findAllRigs(townRoot)
	for _, rig := range rigs {
		// Witness - settings in parent directory (witness/)
		witnessSettings := filepath.Join(rig, "witness", ".claude", "settings.json")
		if _, err := os.Stat(witnessSettings); err == nil {
			files = append(files, witnessSettings)
		}

		// Refinery - settings in parent directory (refinery/)
		refinerySettings := filepath.Join(rig, "refinery", ".claude", "settings.json")
		if _, err := os.Stat(refinerySettings); err == nil {
			files = append(files, refinerySettings)
		}

		// Crew - shared settings in parent directory (crew/)
		crewSettings := filepath.Join(rig, "crew", ".claude", "settings.json")
		if _, err := os.Stat(crewSettings); err == nil {
			files = append(files, crewSettings)
		}

		// Polecats - shared settings in parent directory (polecats/)
		polecatSettings := filepath.Join(rig, "polecats", ".claude", "settings.json")
		if _, err := os.Stat(polecatSettings); err == nil {
			files = append(files, polecatSettings)
		}
	}

	return files
}

// findAllRigs is a shared helper that returns all rig directories within a town.
func findAllRigs(townRoot string) []string {
	var rigs []string

	entries, err := os.ReadDir(townRoot)
	if err != nil {
		return rigs
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Skip non-rig directories
		name := entry.Name()
		if name == "mayor" || name == ".beads" || strings.HasPrefix(name, ".") {
			continue
		}

		rigPath := filepath.Join(townRoot, name)

		// Check if this looks like a rig (has crew/, polecats/, witness/, refinery/, architect/, or qa/)
		markers := []string{"crew", "polecats", "witness", "refinery", "architect", "qa"}
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(rigPath, marker)); err == nil {
				rigs = append(rigs, rigPath)
				break
			}
		}
	}

	return rigs
}

func containsFlag(s, flag string) bool {
	idx := strings.Index(s, flag)
	if idx == -1 {
		return false
	}
	end := idx + len(flag)
	if end >= len(s) {
		return true
	}
	next := s[end]
	return next == '"' || next == ' ' || next == '\'' || next == '\n' || next == '\t'
}

// CustomTypesCheck verifies Gas Town custom types are registered with beads.
type CustomTypesCheck struct {
	FixableCheck
	missingTypes   []string // Cached during Run for use in Fix
	targetBeadsDir string   // Cached during Run for use in Fix
}

// NewCustomTypesCheck creates a new custom types check.
func NewCustomTypesCheck() *CustomTypesCheck {
	return &CustomTypesCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "beads-custom-types",
				CheckDescription: "Check that Gas Town custom types are registered with beads",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks if custom types are properly configured.
func (c *CustomTypesCheck) Run(ctx *CheckContext) *CheckResult {
	// Check if bd command is available
	if _, err := exec.LookPath("bd"); err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "beads not installed (skipped)",
		}
	}

	rigs := findAllRigs(ctx.TownRoot)
	// Always include town root
	rigs = append([]string{ctx.TownRoot}, rigs...)

	// Filter by rig if specified
	if ctx.RigName != "" {
		rigs = []string{ctx.RigPath()}
	}

	var issues []string
	c.missingTypes = nil
	c.targetBeadsDir = "" // Clear cache

	requiredTypes := constants.BeadsCustomTypesList()
	requiredSet := make(map[string]bool)
	for _, t := range requiredTypes {
		requiredSet[t] = true
	}

	for _, rigPath := range rigs {
		beadsDir := beads.ResolveBeadsDir(rigPath)
		if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
			continue
		}

		// Get current custom types configuration
		cmd := exec.Command("bd", "config", "get", "types.custom")
		cmd.Dir = beadsDir
		cmd.Env = doctorConfigEnv(beadsDir)
		output, err := cmd.Output()
		
		configuredTypes := ""
		if err == nil {
			configuredTypes = parseConfigOutput(output)
		}

		configuredSet := make(map[string]bool)
		if configuredTypes != "" {
			for _, t := range strings.Split(configuredTypes, ",") {
				configuredSet[strings.TrimSpace(t)] = true
			}
		}

		var missing []string
		for _, required := range requiredTypes {
			if !configuredSet[required] {
				missing = append(missing, required)
			}
		}

		if len(missing) > 0 {
			relPath, _ := filepath.Rel(ctx.TownRoot, rigPath)
			if relPath == "." {
				relPath = "town root"
			}
			issues = append(issues, fmt.Sprintf("%s missing types: %s", relPath, strings.Join(missing, ", ")))
			
			// For Fix, we'll need to know which beadsDir to update.
			c.targetBeadsDir = beadsDir
		}
	}

	if len(issues) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "All custom types registered across all rigs",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d rig(s) have missing custom types", len(issues)),
		Details: issues,
		FixHint: "Run 'gt doctor --fix' to register missing types",
	}
}

// parseConfigOutput extracts the config value from bd output, filtering out
// informational messages like "Note: ..." that bd may emit to stdout.
func parseConfigOutput(output []byte) string {
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "Note:") && !strings.Contains(line, "(not set)") {
			return line
		}
	}
	return ""
}

// Fix registers the missing custom types across all rigs.
func (c *CustomTypesCheck) Fix(ctx *CheckContext) error {
	rigs := findAllRigs(ctx.TownRoot)
	rigs = append([]string{ctx.TownRoot}, rigs...)
	if ctx.RigName != "" {
		rigs = []string{ctx.RigPath()}
	}

	requiredTypes := constants.BeadsCustomTypesList()

	for _, rigPath := range rigs {
		beadsDir := beads.ResolveBeadsDir(rigPath)
		if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
			continue
		}

		// Ensure metadata is correct so bd connects to the right server
		rigName := filepath.Base(rigPath)
		if rigPath == ctx.TownRoot {
			rigName = "hq"
		}
		_ = doltserver.EnsureMetadata(ctx.TownRoot, rigName)

		getCmd := exec.Command("bd", "config", "get", "types.custom")
		getCmd.Dir = beadsDir
		getCmd.Env = doctorConfigEnv(beadsDir)
		existingOutput, _ := getCmd.Output()

		typeSet := make(map[string]bool)
		if existing := parseConfigOutput(existingOutput); existing != "" {
			for _, typ := range strings.Split(existing, ",") {
				typ = strings.TrimSpace(typ)
				if typ != "" {
					typeSet[typ] = true
				}
			}
		}
		for _, typ := range requiredTypes {
			typeSet[typ] = true
		}

		var merged []string
		for typ := range typeSet {
			merged = append(merged, typ)
		}
		sort.Strings(merged)

		cmd := exec.Command("bd", "config", "set", "types.custom", strings.Join(merged, ","))
		cmd.Dir = beadsDir
		cmd.Env = doctorConfigEnv(beadsDir)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("bd config set types.custom in %s: %s", rigPath, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

// CustomStatusesCheck verifies Gas Town custom statuses are registered with beads.
type CustomStatusesCheck struct {
	FixableCheck
	missingStatuses []string // Cached during Run for use in Fix
	targetBeadsDir  string   // Cached during Run for use in Fix
}

// NewCustomStatusesCheck creates a new custom statuses check.
func NewCustomStatusesCheck() *CustomStatusesCheck {
	return &CustomStatusesCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "beads-custom-statuses",
				CheckDescription: "Check that Gas Town custom statuses are registered with beads",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks if custom statuses are properly configured.
func (c *CustomStatusesCheck) Run(ctx *CheckContext) *CheckResult {
	if _, err := exec.LookPath("bd"); err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "beads not installed (skipped)",
		}
	}

	rigs := findAllRigs(ctx.TownRoot)
	// Always include town root
	rigs = append([]string{ctx.TownRoot}, rigs...)

	// Filter by rig if specified
	if ctx.RigName != "" {
		rigs = []string{ctx.RigPath()}
	}

	var issues []string
	c.missingStatuses = nil
	c.targetBeadsDir = ""

	requiredStatuses := constants.BeadsCustomStatusesList()

	for _, rigPath := range rigs {
		beadsDir := beads.ResolveBeadsDir(rigPath)
		if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
			continue
		}

		// Get current custom statuses configuration
		cmd := exec.Command("bd", "config", "get", "status.custom")
		cmd.Dir = beadsDir
		cmd.Env = doctorConfigEnv(beadsDir)
		output, err := cmd.Output()

		configuredStatuses := ""
		if err == nil {
			configuredStatuses = parseConfigOutput(output)
		}

		configuredSet := make(map[string]bool)
		if configuredStatuses != "" {
			for _, s := range strings.Split(configuredStatuses, ",") {
				configuredSet[strings.TrimSpace(s)] = true
			}
		}

		var missing []string
		for _, required := range requiredStatuses {
			if !configuredSet[required] {
				missing = append(missing, required)
			}
		}

		if len(missing) > 0 {
			relPath, _ := filepath.Rel(ctx.TownRoot, rigPath)
			if relPath == "." {
				relPath = "town root"
			}
			issues = append(issues, fmt.Sprintf("%s missing statuses: %s", relPath, strings.Join(missing, ", ")))
			c.targetBeadsDir = beadsDir
		}
	}

	if len(issues) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "All custom statuses registered across all rigs",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d rig(s) have missing custom statuses", len(issues)),
		Details: issues,
		FixHint: "Run 'gt doctor --fix' to register missing statuses",
	}
}

// Fix registers the missing custom statuses across all rigs.
func (c *CustomStatusesCheck) Fix(ctx *CheckContext) error {
	rigs := findAllRigs(ctx.TownRoot)
	rigs = append([]string{ctx.TownRoot}, rigs...)
	if ctx.RigName != "" {
		rigs = []string{ctx.RigPath()}
	}

	requiredStatuses := constants.BeadsCustomStatusesList()

	for _, rigPath := range rigs {
		beadsDir := beads.ResolveBeadsDir(rigPath)
		if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
			continue
		}

		// Ensure metadata is correct so bd connects to the right server
		rigName := filepath.Base(rigPath)
		if rigPath == ctx.TownRoot {
			rigName = "hq"
		}
		_ = doltserver.EnsureMetadata(ctx.TownRoot, rigName)

		getCmd := exec.Command("bd", "config", "get", "status.custom")
		getCmd.Dir = beadsDir
		getCmd.Env = doctorConfigEnv(beadsDir)
		existingOutput, _ := getCmd.Output()

		statusSet := make(map[string]bool)
		if existing := parseConfigOutput(existingOutput); existing != "" {
			for _, s := range strings.Split(existing, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					statusSet[s] = true
				}
			}
		}
		for _, s := range requiredStatuses {
			statusSet[s] = true
		}

		var merged []string
		for s := range statusSet {
			merged = append(merged, s)
		}
		sort.Strings(merged)

		cmd := exec.Command("bd", "config", "set", "status.custom", strings.Join(merged, ","))
		cmd.Dir = beadsDir
		cmd.Env = doctorConfigEnv(beadsDir)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("bd config set status.custom in %s: %s", rigPath, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func doctorConfigBeadsDir(ctx *CheckContext) string {
	workDir := ctx.TownRoot
	if ctx.RigName != "" {
		workDir = ctx.RigPath()
	}
	return beads.ResolveBeadsDir(workDir)
}

func doctorConfigEnv(beadsDir string) []string {
	env := stripEnvPrefixes(os.Environ(), "BEADS_DIR=", "BEADS_DB=", "BEADS_DOLT_SERVER_DATABASE=")
	env = append(env, "BEADS_DIR="+beadsDir)
	if dbEnv := beads.DatabaseEnv(beadsDir); dbEnv != "" {
		env = append(env, dbEnv)
	}
	return env
}

func stripEnvPrefixes(env []string, prefixes ...string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		skip := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
