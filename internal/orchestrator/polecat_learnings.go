package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxLearningRuleChars    = 140
	MaxProjectLearnings     = 8
	MaxTownStackLearnings   = 8
	MaxLearningsPromptBytes = 800
)

// LearningEntry represents a single concise self-learning item recorded by or for Polecat.
type LearningEntry struct {
	Stack     string `json:"stack,omitempty"`
	Rule      string `json:"rule"`
	Source    string `json:"source,omitempty"` // "agent", "verify_fix", "qa_rework"
	BeadPath  string `json:"bead_path,omitempty"`
	Hits      int    `json:"hits"`
	UpdatedAt string `json:"updated_at"`
}

// LearningsStore persists a bounded list of self-improvement rules.
type LearningsStore struct {
	Version int             `json:"version"`
	Entries []LearningEntry `json:"entries"`
}

// ProjectLearningsPath returns {townRoot}/{rig}/mayor/rig/.gastown/polecat-learnings.json.
func ProjectLearningsPath(townRoot, rig string) string {
	if townRoot == "" || rig == "" {
		return ""
	}
	return filepath.Join(townRoot, rig, "mayor", "rig", ".gastown", "polecat-learnings.json")
}

// TownStackLearningsPath returns {townRoot}/orchestrator/learnings/{stack}.json.
func TownStackLearningsPath(townRoot, stack string) string {
	stack = normalizeStackName(stack)
	if townRoot == "" || stack == "" {
		return ""
	}
	return filepath.Join(townRoot, TownAssetsSubdir, "learnings", stack+".json")
}

func normalizeStackName(stack string) string {
	s := strings.ToLower(strings.TrimSpace(stack))
	switch s {
	case "go", "golang":
		return "go"
	case "py", "python", "python3":
		return "python"
	case "ts", "tsx", "js", "jsx", "javascript", "typescript", "web", "node", "nodejs":
		return "typescript"
	case "sql", "sqlite", "postgres", "postgresql":
		return "sql"
	case "docker", "compose", "dockerfile":
		return "docker"
	default:
		return ""
	}
}

// SanitizeLearningRule normalizes a learning string to a single concise line (<= MaxLearningRuleChars).
func SanitizeLearningRule(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimLeft(s, "-*• \t")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, "`\"")
	if len(s) < 10 {
		return ""
	}
	if len(s) > MaxLearningRuleChars {
		s = strings.TrimSpace(s[:MaxLearningRuleChars-3]) + "..."
	}
	return s
}

func normalizeRuleKey(rule string) string {
	return strings.ToLower(strings.TrimRight(SanitizeLearningRule(rule), ".!?;:"))
}

// InferLearningStack determines the canonical stack name for a learning entry.
func InferLearningStack(explicitStack, beadPath string, v WorkflowValidation) string {
	if st := normalizeStackName(explicitStack); st != "" {
		return st
	}
	scoped := v.ForActivePhase()
	if beadPath != "" {
		if stacks := stacksForFilePath(beadPath, scoped); len(stacks) > 0 {
			return stacks[0]
		}
	}
	if stacks := stacksForValidation(scoped); len(stacks) > 0 {
		return stacks[0]
	}
	if stacks := stacksForValidation(v); len(stacks) > 0 {
		return stacks[0]
	}
	return ""
}

// RecordPolecatLearning saves a concise learning rule to both the project-scoped store
// (.gastown/polecat-learnings.json) and the town-scoped stack store (orchestrator/learnings/<stack>.json).
func RecordPolecatLearning(townRoot, rig, stack, rule, source, beadPath string, v WorkflowValidation) error {
	cleanRule := SanitizeLearningRule(rule)
	if cleanRule == "" {
		return nil
	}
	resolvedStack := InferLearningStack(stack, beadPath, v)
	if source == "" {
		source = "agent"
	}
	now := time.Now().UTC().Format(time.RFC3339)

	if projPath := ProjectLearningsPath(townRoot, rig); projPath != "" {
		entry := LearningEntry{
			Stack:     resolvedStack,
			Rule:      cleanRule,
			Source:    source,
			BeadPath:  filepath.ToSlash(strings.TrimSpace(beadPath)),
			Hits:      1,
			UpdatedAt: now,
		}
		if err := upsertLearningFile(projPath, entry, MaxProjectLearnings); err != nil {
			return err
		}
	}

	if townPath := TownStackLearningsPath(townRoot, resolvedStack); townPath != "" {
		townEntry := LearningEntry{
			Stack:     resolvedStack,
			Rule:      cleanRule,
			Source:    source,
			Hits:      1,
			UpdatedAt: now,
		}
		if err := upsertLearningFile(townPath, townEntry, MaxTownStackLearnings); err != nil {
			return err
		}
	}
	return nil
}

func loadLearningsFile(path string) LearningsStore {
	var store LearningsStore
	store.Version = 1
	if path == "" {
		return store
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return store
	}
	_ = json.Unmarshal(data, &store)
	if store.Version == 0 {
		store.Version = 1
	}
	return store
}

func upsertLearningFile(path string, incoming LearningEntry, maxEntries int) error {
	store := loadLearningsFile(path)
	key := normalizeRuleKey(incoming.Rule)
	if key == "" {
		return nil
	}

	foundIdx := -1
	for i, existing := range store.Entries {
		if normalizeRuleKey(existing.Rule) == key {
			foundIdx = i
			break
		}
	}

	if foundIdx >= 0 {
		updated := store.Entries[foundIdx]
		updated.Hits++
		updated.UpdatedAt = incoming.UpdatedAt
		if updated.Stack == "" && incoming.Stack != "" {
			updated.Stack = incoming.Stack
		}
		if updated.BeadPath == "" && incoming.BeadPath != "" {
			updated.BeadPath = incoming.BeadPath
		}
		// Move reinforced entry to the end (most recent)
		store.Entries = append(store.Entries[:foundIdx], store.Entries[foundIdx+1:]...)
		store.Entries = append(store.Entries, updated)
	} else {
		if incoming.Hits <= 0 {
			incoming.Hits = 1
		}
		store.Entries = append(store.Entries, incoming)
	}

	for len(store.Entries) > maxEntries {
		evictIdx := 0
		minHits := store.Entries[0].Hits
		for i := 1; i < len(store.Entries); i++ {
			if store.Entries[i].Hits < minHits {
				minHits = store.Entries[i].Hits
				evictIdx = i
			}
		}
		store.Entries = append(store.Entries[:evictIdx], store.Entries[evictIdx+1:]...)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// FormatPolecatLearningsBlock renders concise project-scoped and town-scoped stack learnings
// for the active phase/bead, strictly filtered by stack and capped at MaxLearningsPromptBytes.
func FormatPolecatLearningsBlock(townRoot, rig string, v WorkflowValidation) string {
	activeStacks := DetectActiveCodingStacks(townRoot, rig, v)
	allowedStacks := make(map[string]bool, len(activeStacks))
	for _, s := range activeStacks {
		allowedStacks[s] = true
	}

	var combined []LearningEntry
	seen := make(map[string]bool)

	addEntry := func(e LearningEntry, requireStackMatch bool) {
		st := normalizeStackName(e.Stack)
		if len(allowedStacks) > 0 {
			if st != "" && !allowedStacks[st] {
				return
			}
			if requireStackMatch && st == "" {
				return
			}
		}
		k := normalizeRuleKey(e.Rule)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		combined = append(combined, e)
	}

	// 1. Project-scoped learnings (most recent first)
	if projPath := ProjectLearningsPath(townRoot, rig); projPath != "" {
		projStore := loadLearningsFile(projPath)
		for i := len(projStore.Entries) - 1; i >= 0; i-- {
			addEntry(projStore.Entries[i], false)
		}
	}

	// 2. Town-scoped learnings for active stack(s) only (most recent first)
	for _, stack := range activeStacks {
		if townPath := TownStackLearningsPath(townRoot, stack); townPath != "" {
			townStore := loadLearningsFile(townPath)
			for i := len(townStore.Entries) - 1; i >= 0; i-- {
				e := townStore.Entries[i]
				if e.Stack == "" {
					e.Stack = stack
				}
				addEntry(e, true)
			}
		}
	}

	if len(combined) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Self-learning notes (from prior iterations — do not repeat these mistakes)\n\n")
	for _, e := range combined {
		var line string
		if e.Stack != "" {
			line = fmt.Sprintf("- [%s] %s\n", e.Stack, e.Rule)
		} else {
			line = fmt.Sprintf("- %s\n", e.Rule)
		}
		if b.Len()+len(line) > MaxLearningsPromptBytes {
			break
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}

// ExtractVerifyFixLearning distils a concise 1-line lesson when a verify failure on beadPath
// is subsequently resolved and passes verification.
func ExtractVerifyFixLearning(failOutput, beadPath string, v WorkflowValidation) string {
	if strings.TrimSpace(failOutput) == "" {
		return ""
	}
	lines := strings.Split(failOutput, "\n")
	var signalLine string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "exit status") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "undefined:") ||
			strings.Contains(lower, "cannot use ") ||
			strings.Contains(lower, "too many arguments") ||
			strings.Contains(lower, "not enough arguments") ||
			strings.Contains(lower, "redeclared in this block") ||
			strings.Contains(lower, "imported and not used") ||
			strings.Contains(lower, "importerror:") ||
			strings.Contains(lower, "modulenotfounderror:") ||
			strings.Contains(lower, "typeerror:") ||
			strings.Contains(lower, "attributeerror:") ||
			strings.Contains(lower, "assertionerror:") ||
			strings.Contains(lower, "referenceerror:") ||
			strings.Contains(lower, "syntaxerror:") ||
			strings.Contains(lower, "--- fail:") {
			signalLine = line
			break
		}
	}
	if signalLine == "" {
		return ""
	}
	prefix := "Avoid verify error"
	if base := filepath.Base(strings.TrimSpace(beadPath)); base != "" && base != "." {
		prefix = fmt.Sprintf("Avoid in %s", base)
	}
	return SanitizeLearningRule(fmt.Sprintf("%s: %s", prefix, signalLine))
}

// ExtractQAReworkLearning distils a concise 1-line lesson from a resolved QA/test review rework.
func ExtractQAReworkLearning(fromState, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	// Take the first sentence or line of the rework summary
	for _, sep := range []string{"\n", ". ", "; "} {
		if idx := strings.Index(summary, sep); idx > 10 {
			summary = summary[:idx]
			break
		}
	}
	prefix := "QA lesson"
	if fromState == "test_review" {
		prefix = "Test review lesson"
	}
	return SanitizeLearningRule(fmt.Sprintf("%s: %s", prefix, summary))
}
