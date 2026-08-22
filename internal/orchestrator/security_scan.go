package orchestrator

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SecretFinding records a potential secret discovered in rig code.
type SecretFinding struct {
	Path      string
	Line      int
	Kind      string
	Match     string
	FullLine  string
}

// ScanRigSecrets scans the rig's layout root for potential secrets and credentials.
// It follows bench-style deterministic checks with an allow-secret escape hatch.
func ScanRigSecrets(townRoot, rig, layoutRoot string) []SecretFinding {
	var findings []SecretFinding
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")

	if layoutRoot != "" && strings.HasPrefix(layoutRoot, "./") {
		layoutRoot = strings.TrimPrefix(layoutRoot, "./")
	} else if layoutRoot == "" {
		// Fall back to reading from workflow profile; if unavailable, scan whole mayor/rig.
		layoutRoot = "."
	}
	scanDir := filepath.Join(rigDir, layoutRoot)

	// Directories and files to skip entirely.
	skipDirs := map[string]bool{
		".git":       true,
		"node_modules": true,
		"vendor":     true,
		".venv":      true,
		"venv":       true,
		"__pycache__": true,
		"dist":       true,
		"build":      true,
	}
	skipFiles := map[string]bool{
		"go.sum":       true,
		"*.lock.json":  true,
		"go.mod":       true, // skip module declarations — generally not secrets, but avoid noise
	}

	// Compile regex patterns for each secret kind.
	patterns := map[string]*regexp.Regexp{
		"private_key": regexp.MustCompile(`-----BEGIN\s+(?:[A-Z]+\s+)*PRIVATE\s+KEY-----`),
		"aws_key":     regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		"github_token": regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36}`),
		"slack_token": regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
		"google_key":  regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`),
		"jwt":         regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}\b`),
		"secret_assign": regexp.MustCompile(`(?i)(?:api[_-]?key|secret|password|passwd|token|auth[_-]?token|access[_-]?token)\s*[:=]\s*["'][^"'{]{8,}["']`),
		"fictional_555": regexp.MustCompile(`(?:\+1[\s.-]?)?\(?\d{3}\)?[\s.-]\d{3}[\s.-]\d{4}\b`),
	}

	// Placeholder prefixes/values to suppress (avoid false positives on tutorials, examples).
	placeholderPrefixes := []string{
		"your-", "your_", "example", "changeme", "<", ">", "${", "process.env", "os.Getenv",
		"xxx", "your-api", "your-api-key",
	}

	// Walk the scan directory.
	err := filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories in the skip list.
		if d.IsDir() {
			rel, _ := filepath.Rel(scanDir, path)
			parts := strings.Split(rel, string(os.PathSeparator))
			for _, p := range parts {
				if skipDirs[strings.ToLower(p)] {
					return fs.SkipDir
				}
			}
		}

		// Skip files matching skip patterns.
		if !d.IsDir() {
			rel, _ := filepath.Rel(scanDir, path)
			base := filepath.Base(rel)
			matched := false
			for p := range skipFiles {
				matched, _ = filepath.Match(p, base)
				if matched {
					return nil
				}
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if len(data) > 1_000_000 {
			// Skip files > 1 MB to avoid scanning huge build artifacts.
			return nil
		}

		content := string(data)
		lines := strings.Split(content, "\n")

		for li, line := range lines {
			trimmed := strings.TrimRight(line, " \t\r")
			if trimmed == "" {
				continue
			}

			// Check allow-secret escape hatch: if line contains "allow-secret: <reason>",
			// skip ALL checks for that line (reason required, bare marker not accepted).
			if matched := strings.HasPrefix(strings.ToLower(trimmed), "allow-secret:"); matched {
				// Bare marker without reason is also OK to skip (bench-style: accept as escape hatch).
				if strings.HasSuffix(strings.ToLower(trimmed), "allow-secret") {
					continue
				}
				// Has reason — skip this line entirely.
				continue
			}

			// Check each pattern.
			for kind, re := range patterns {
				matches := re.FindAllStringSubmatch(trimmed, -1)
				for _, m := range matches {
					matchStr := m[0]

					// Skip obviously fictional 555 phone numbers (bench-style).
					if kind == "fictional_555" && isFictional555(matchStr) {
						continue
					}

					// Skip placeholder prefixes/values.
					isPlaceholder := false
					for _, prefix := range placeholderPrefixes {
						if strings.HasPrefix(strings.ToLower(matchStr), strings.ToLower(prefix)) {
							isPlaceholder = true
							break
						}
					}
					if isPlaceholder {
						continue
					}

					// Skip JWTs that are suspiciously short or clearly placeholder.
					if kind == "jwt" && len(matchStr) < 100 {
						continue
					}

					findings = append(findings, SecretFinding{
						Path:      path,
						Line:      li + 1,
						Kind:      kind,
						Match:     matchStr,
						FullLine:  trimmed,
					})
				}
			}
		}

		return nil
	})

	if err != nil {
		_ = fmt.Errorf("scan rig secrets: %w", err)
	}
	return findings
}

// isFictional555 checks whether a phone number matches the 555-01xx block
// reserved for fiction (per bench's check-secrets.mjs logic).
func isFictional555(s string) bool {
	digits := strings.ReplaceAll(s, "-", "")
	digits = strings.ReplaceAll(digits, " ", "")
	digits = strings.ReplaceAll(digits, ".", "")
	if len(digits) != 10 {
		return false
	}
	if digits[:3] != "555" {
		return false
	}
	return true
}

// FormatSecretFindings formats a list of SecretFinding into a human-readable
// error message suitable for QA rejection feedback.
func FormatSecretFindings(findings []SecretFinding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Security validation found potential secrets/credentials:\n")
	for _, f := range findings {
		b.WriteString(fmt.Sprintf("  %s (%s:%d): %s\n", f.Kind, f.Path, f.Line, f.Match))
	}
	b.WriteString("\nTo allow a secret, add `allow-secret: <reason>` on the same line.\n")
	b.WriteString("Refer to " + SECURITY_STANDARDSPath + " for full standards.")
	return b.String()
}

// SECURITY_STANDARDSPath is the path relative to the orchestrator package where
// the SECURITY_STANDARDS.md file is embedded for agent reference.
const SECURITY_STANDARDSPath = "SECURITY_STANDARDS.md"

// ScanRigSecretsForWorkflow wraps ScanRigSecrets to use the active phase's layout root.
func ScanRigSecretsForWorkflow(townRoot, rig string, v WorkflowValidation) []SecretFinding {
	layoutRoot := v.LayoutRoot
	if layoutRoot == "" {
		layoutRoot = "."
	}
	return ScanRigSecrets(townRoot, rig, layoutRoot)
}