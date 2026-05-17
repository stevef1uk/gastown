package agentenv

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RepairRequirementsFile removes shell/command lines mistakenly written into requirements.txt.
func RepairRequirementsFile(path string) (changed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	cleaned := SanitizeRequirementsText(string(data))
	if cleaned == string(data) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(cleaned), 0644); err != nil {
		return false, err
	}
	return true, nil
}

// RepairRequirementsUnder repairs requirements.txt files under workDir (skips .venv, .git).
func RepairRequirementsUnder(workDir string) (repaired []string, err error) {
	if workDir == "" {
		return nil, nil
	}
	err = filepath.WalkDir(workDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".venv", "venv", "__pycache__", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(path) != "requirements.txt" {
			return nil
		}
		changed, rerr := RepairRequirementsFile(path)
		if rerr != nil {
			return rerr
		}
		if changed {
			rel, _ := filepath.Rel(workDir, path)
			repaired = append(repaired, rel)
		}
		return nil
	})
	return repaired, err
}

// SanitizeRequirementsText keeps valid pip requirement lines and drops shell invocations.
func SanitizeRequirementsText(content string) string {
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			kept = append(kept, "")
			continue
		}
		if strings.HasPrefix(trim, "#") {
			kept = append(kept, line)
			continue
		}
		if norm := normalizeRequirementLine(trim); norm != "" {
			kept = append(kept, norm)
		}
	}
	out := strings.Join(kept, "\n")
	return strings.TrimRight(out, "\n") + "\n"
}

func normalizeRequirementLine(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return ""
	}
	if m := regexp.MustCompile(`(?i)(?:^|[\s/])python3?\s+-m\s+([a-z0-9_.-]+)\s*$`).FindStringSubmatch(line); len(m) > 1 {
		return m[1]
	}
	if strings.Contains(lower, " -m ") {
		return ""
	}
	if strings.HasPrefix(lower, "pip install") || strings.HasPrefix(lower, "cd ") {
		return ""
	}
	if strings.Contains(line, " ") && !strings.Contains(line, "==") && !strings.Contains(line, ">=") {
		fields := strings.Fields(line)
		if len(fields) > 0 && looksLikeShellVerb(fields[0]) {
			return ""
		}
	}
	return strings.TrimSpace(line)
}

func looksLikeShellVerb(w string) bool {
	switch strings.ToLower(w) {
	case "python3", "python", "pip", "pytest", "bash", "sh", "cd", "export":
		return true
	}
	return false
}

// RequirementsPathFromPipInstall extracts the -r path from a pip install command, if any.
func RequirementsPathFromPipInstall(cmd string) string {
	re := regexp.MustCompile(`(?i)-r\s+("([^"]+)"|'([^']+)'|(\S+))`)
	m := re.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	for i := 2; i < len(m); i++ {
		if m[i] != "" {
			return m[i]
		}
	}
	return ""
}
