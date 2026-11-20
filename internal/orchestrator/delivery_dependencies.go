package orchestrator

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DependencyBlock is evidence from a failed build, not permission for Polecat
// to move files between phases. The architect repairs the canonical contract.
type DependencyBlock struct {
	Consumer   string `json:"consumer"`
	Dependency string `json:"dependency"`
	Evidence   string `json:"evidence"`
}

func activePhaseOwnsDependencyPath(v WorkflowValidation, file string) bool {
	phase, ok := v.ActivePhase()
	return ok && containsString(phase.RequiredFiles, file)
}

func dependencyOwners(v WorkflowValidation) map[string]int {
	owners := map[string]int{}
	for i, phase := range v.DeliveryPhases {
		for _, file := range phase.RequiredFiles {
			file = path.Clean(filepath.ToSlash(file))
			if _, exists := owners[file]; !exists {
				owners[file] = i // repeated files are refinements, not new prerequisites
			}
		}
	}
	return owners
}

// ValidateDependencyBlock refuses ordinary missing-file/toolchain failures and
// ambiguous ownership: only a concrete active consumer -> future provider is a
// phase planning defect. Earlier-phase failures retain their existing recovery.
func ValidateDependencyBlock(v WorkflowValidation, b DependencyBlock) error {
	if strings.TrimSpace(b.Evidence) == "" {
		return fmt.Errorf("dependency_blocked requires the failed command and diagnostic evidence")
	}
	active, ok := v.ActivePhase()
	if !ok || !containsString(active.RequiredFiles, b.Consumer) {
		return fmt.Errorf("dependency_blocked consumer %q is not an active-phase required file", b.Consumer)
	}
	owners := dependencyOwners(v)
	provider, ok := owners[b.Dependency]
	if !ok {
		return fmt.Errorf("dependency_blocked provider %q has no delivery-phase owner", b.Dependency)
	}
	for i, phase := range v.DeliveryPhases {
		if phase.ID == active.ID && provider <= i {
			return fmt.Errorf("dependency %q is not assigned to a future phase; implement or repair its owning bead", b.Dependency)
		}
	}
	return nil
}

var dependencyTokenRE = regexp.MustCompile("[A-Za-z0-9_./@+-]+")

func withoutDependencyAnnotations(text string) string {
	var result strings.Builder
	for len(text) > 0 {
		start := strings.IndexByte(text, '(')
		if start < 0 {
			result.WriteString(text)
			break
		}
		result.WriteString(text[:start])
		depth, end := 0, -1
		for i := start; i < len(text); i++ {
			switch text[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			result.WriteString(text[start:])
			break
		}
		result.WriteByte(' ')
		text = text[end+1:]
	}
	return result.String()
}

// planFileDependencies reads only explicitly labelled Depends on fields and
// exact known paths/IDs. Parenthetical explanations,
// filenames with ambiguous basenames, and external packages never invent edges.
func planFileDependencies(doc string, files []string) map[string][]string {
	known := map[string]bool{}
	for _, file := range files {
		known[file] = true
	}
	ids := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "### ") {
			id, rest, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "### ")), ":")
			if ok {
				for _, token := range dependencyTokenRE.FindAllString(rest, -1) {
					if known[token] {
						ids[strings.Trim(id, " `")] = token
						break
					}
				}
			}
		}
	}
	edges := map[string][]string{}
	consumer := ""
	for _, line := range strings.Split(doc, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			consumer = ""
			if strings.HasPrefix(trim, "### ") {
				id, _, _ := strings.Cut(strings.TrimPrefix(trim, "### "), ":")
				consumer = ids[strings.Trim(id, " `")]
			}
		}
		label, rest, ok := strings.Cut(strings.Trim(trim, " -*"), ":")
		if consumer == "" || !ok || !strings.EqualFold(strings.Trim(label, " *"), "Depends on") {
			continue
		}
		for _, token := range dependencyTokenRE.FindAllString(withoutDependencyAnnotations(rest), -1) {
			provider := token
			if p := ids[token]; p != "" {
				provider = p
			}
			if known[provider] && provider != consumer && !containsString(edges[consumer], provider) {
				edges[consumer] = append(edges[consumer], provider)
			}
		}
	}
	return edges
}

func deliveryFileDependencies(rigDir string, v WorkflowValidation) map[string][]string {
	files := append([]string(nil), v.RequiredFiles...)
	for _, phase := range v.DeliveryPhases {
		files = append(files, phase.RequiredFiles...)
	}
	doc := ""
	if rigDir != "" {
		doc = readRigDoc(rigDir, "plan.md")
	}
	edges := planFileDependencies(doc, files)
	for _, phase := range v.DeliveryPhases {
		if phase.ID != v.ActivePhaseID() {
			continue
		}
		for consumer, providers := range phase.FileDependencies {
			for _, provider := range providers {
				if !containsString(edges[consumer], provider) {
					edges[consumer] = append(edges[consumer], provider)
				}
			}
		}
	}
	// TypeScript/JSX compilation requires the nearest declared package manifest.
	// Only known contract paths participate; plain static JS/CSS and unrelated
	// test packages do not acquire guessed dependencies.
	for _, file := range files {
		switch strings.ToLower(path.Ext(file)) {
		case ".ts", ".tsx", ".jsx":
		default:
			continue
		}
		provider := ""
		for _, candidate := range files {
			if path.Base(candidate) == "package.json" && strings.HasPrefix(file, path.Dir(candidate)+"/") && len(candidate) > len(provider) {
				provider = candidate
			}
		}
		if provider != "" && !containsString(edges[file], provider) {
			edges[file] = append(edges[file], provider)
		}
	}
	return edges
}

// verificationProjectInputs recognizes literal paths and static directory
// changes, plus the manifest used by Node package commands. It is deliberately
// not a shell interpreter: dynamic paths and arbitrary scripts need review or
// a declared file_dependencies edge.
func verificationProjectInputs(command string) []string {
	var inputs []string
	cwd := "."
	for _, step := range regexp.MustCompile(`&&|;|\n`).Split(command, -1) {
		words := strings.Fields(strings.TrimSpace(step))
		if len(words) == 0 {
			continue
		}
		if words[0] == "cd" && len(words) == 2 {
			dir := strings.Trim(words[1], "\"'")
			if strings.ContainsAny(dir, "$`()*") {
				cwd = ""
			} else if cwd != "" {
				cwd = path.Join(cwd, dir)
			}
			continue
		}
		if words[0] == "echo" || words[0] == "printf" || words[0] == "mkdir" {
			continue
		}
		for _, token := range dependencyTokenRE.FindAllString(step, -1) {
			inputs = append(inputs, token)
			if cwd != "" {
				inputs = append(inputs, path.Join(cwd, token))
			}
		}
		if cwd != "" && (words[0] == "npm" || words[0] == "pnpm" || words[0] == "yarn") {
			dir := cwd
			for i, word := range words {
				if (word == "--prefix" || word == "--dir" || word == "-C" || word == "--cwd") && i+1 < len(words) {
					dir = path.Join(cwd, strings.Trim(words[i+1], "\"'"))
				}
			}
			inputs = append(inputs, path.Join(dir, "package.json"))
		}
	}
	return inputs
}

// dependencyBuildOrder is a stable topological order. Existing profile/toolchain
// ordering remains the tie-breaker. No source execution or package installation.
func dependencyBuildOrder(files []string, edges map[string][]string) ([]string, error) {
	known := map[string]bool{}
	for _, file := range files {
		known[file] = true
	}
	visited := map[string]int{}
	var ordered, stack []string
	var visit func(string) error
	visit = func(file string) error {
		if visited[file] == 1 {
			return fmt.Errorf("file dependency cycle: %s", strings.Join(append(stack, file), " -> "))
		}
		if visited[file] == 2 {
			return nil
		}
		visited[file] = 1
		stack = append(stack, file)
		for _, dep := range edges[file] {
			if known[dep] {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		visited[file] = 2
		ordered = append(ordered, file)
		return nil
	}
	for _, file := range files {
		if err := visit(file); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// DeliveryDependencyProblems checks the whole plan, including future phases.
// It never moves files automatically: phase ownership and acceptance commands
// must be amended together in architecture.md and reviewed again.
func DeliveryDependencyProblems(rigDir string, v WorkflowValidation) []string {
	problems := deliveryGraphProblems(v)
	owners := dependencyOwners(v)
	if len(v.DeliveryPhases) == 0 {
		if _, err := dependencyBuildOrder(v.RequiredFiles, deliveryFileDependencies(rigDir, v)); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for i, phase := range v.DeliveryPhases {
		// Refinements can change dependencies of a repeated file. Apply each
		// canonical phase's edges only to that phase; the current plan supplements
		// only the active phase, not every future refinement of the same path.
		phaseV := v
		phaseV.ActivePhaseIDField = phase.ID
		edges := deliveryFileDependencies("", phaseV)
		if phase.ID == v.ActivePhaseID() {
			edges = deliveryFileDependencies(rigDir, phaseV)
		}
		if _, err := dependencyBuildOrder(phase.RequiredFiles, edges); err != nil {
			problems = append(problems, fmt.Sprintf("phase %q: %v", phase.ID, err))
		}
		for _, dep := range phase.DependsOn {
			for j, other := range v.DeliveryPhases {
				if dep == other.ID && j > i {
					problems = append(problems, fmt.Sprintf("phase %q runs before its prerequisite phase %q", phase.ID, dep))
				}
			}
		}
		for consumer, providers := range phase.FileDependencies {
			if !containsString(phase.RequiredFiles, consumer) {
				problems = append(problems, fmt.Sprintf("phase %q declares dependencies for non-owned file %q", phase.ID, consumer))
			}
			for _, provider := range providers {
				if _, ok := owners[provider]; !ok {
					problems = append(problems, fmt.Sprintf("%s depends on unowned project file %s", consumer, provider))
				}
			}
		}
		for _, consumer := range phase.RequiredFiles {
			for _, dep := range edges[consumer] {
				if owner, ok := owners[dep]; ok && owner > i {
					problems = append(problems, fmt.Sprintf("phase %q: %s requires %s, assigned to future phase %q", phase.ID, consumer, dep, v.DeliveryPhases[owner].ID))
				}
			}
		}
		// Exact path tokens in the acceptance command are concrete inputs. Do not
		// infer imports, network endpoints, or dependencies from arbitrary prose.
		for _, token := range verificationProjectInputs(phase.QAVerifyCommand) {
			if owner, ok := owners[token]; ok && owner > i {
				problems = append(problems, fmt.Sprintf("phase %q verification requires %s from future phase %q", phase.ID, token, v.DeliveryPhases[owner].ID))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// DeliveryPlanDependencyProblems returns problems introduced by plan.md when
// the canonical delivery contract is independently valid.
func DeliveryPlanDependencyProblems(rigDir string, v WorkflowValidation) []string {
	if rigDir == "" || len(DeliveryDependencyProblems("", v)) != 0 {
		return nil
	}
	return DeliveryDependencyProblems(rigDir, v)
}

func planDependencyRepairFeedback(problems []string) string {
	return "The implementation plan has dependency defects:\n- " + strings.Join(problems, "\n- ") + "\n\nAmend the Depends on fields in plan.md to remove the unsupported edges and make the combined plan and delivery contract buildable. Preserve the canonical delivery contract, existing implementation, tests, completed phases, and real bead IDs. If a necessary prerequisite requires a delivery contract change, escalate to the architect instead of hiding it."
}

func dependencyRepairFeedback(problems []string) string {
	return "Delivery dependency plan is not buildable:\n- " + strings.Join(problems, "\n- ") + "\n\nAmend the single canonical delivery_phases block in architecture.md: move prerequisites earlier, move dependent work later, or merge coupled phases. Update file_dependencies, depends_on and qa_verify_command together. Preserve existing implementation, tests, databases, real bead IDs and unaffected completed phases. Do not merely retry the failing build. Planning sync will refresh beads and plan.md; regenerate and review the test plan before resuming Polecat."
}

func dependencyReportProblem(v WorkflowValidation, summary string) (string, error) {
	var block DependencyBlock
	if err := json.Unmarshal([]byte(summary), &block); err != nil {
		return "", fmt.Errorf("dependency_blocked requires a structured dependency report: %w", err)
	}
	if err := ValidateDependencyBlock(v, block); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s requires future-phase file %s. Failed verification evidence: %s", block.Consumer, block.Dependency, block.Evidence), nil
}
