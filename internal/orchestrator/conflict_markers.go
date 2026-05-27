package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var conflictMarkerPrefixes = []string{
	"<<<<<<< ",
	"=======",
	">>>>>>> ",
}

// IsConflictMarkerLine reports whether a line is an edit/merge conflict artifact.
func IsConflictMarkerLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	for _, prefix := range conflictMarkerPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// ScrubFileConflictMarkers removes edit/merge conflict markers from a file.
// Returns true if the file was modified, and the number of removed lines.
func ScrubFileConflictMarkers(absPath string) (changed bool, removedCount int, err error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return false, 0, fmt.Errorf("read %s: %w", absPath, err)
	}
	result, removed := scrubConflictMarkers(string(data))
	if removed == 0 {
		return false, 0, nil
	}
	if err := os.WriteFile(absPath, []byte(result), 0644); err != nil {
		return false, removed, fmt.Errorf("write %s: %w", absPath, err)
	}
	return true, removed, nil
}

func scrubConflictMarkers(content string) (cleaned string, removedCount int) {
	lines := strings.Split(content, "\n")
	var cleanedLines []string
	removed := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isMarker := false
		for _, prefix := range conflictMarkerPrefixes {
			if strings.HasPrefix(trimmed, prefix) {
				isMarker = true
				break
			}
		}
		if isMarker {
			removed++
			continue
		}
		cleanedLines = append(cleanedLines, line)
	}

	if removed == 0 {
		return content, 0
	}
	return strings.Join(cleanedLines, "\n"), removed
}

// ScrubPackageConflictMarkers scans all .go files in a package directory
// and removes conflict markers from any file that contains them.
// Returns the list of files modified.
func ScrubPackageConflictMarkers(pkgDir string) ([]string, error) {
	var modified []string
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		abs := filepath.Join(pkgDir, entry.Name())
		changed, _, err := ScrubFileConflictMarkers(abs)
		if err != nil {
			return modified, err
		}
		if changed {
			modified = append(modified, entry.Name())
		}
	}
	return modified, nil
}
