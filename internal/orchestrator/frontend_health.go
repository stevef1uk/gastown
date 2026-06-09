package orchestrator

import (
	"fmt"
	"regexp"
	"strings"
)

var jsDOMContentLoadedRE = regexp.MustCompile(`document\.addEventListener\s*\(\s*["']DOMContentLoaded["']`)

// CheckJavaScriptFileHealthy rejects concatenated or marker-corrupted app.js files common
// when agents append partial EDIT blocks instead of replacing the whole file.
func CheckJavaScriptFileHealthy(data []byte) error {
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("javascript file is empty")
	}
	for _, m := range []string{
		"<<<<<<< SEARCH", ">>>>>>> REPLACE", "<<<<<<<", "=======", ">>>>>>>",
	} {
		if strings.Contains(text, m) {
			return fmt.Errorf("javascript file contains unresolved edit markers")
		}
	}
	if n := len(jsDOMContentLoadedRE.FindAllString(text, -1)); n > 1 {
		return fmt.Errorf("javascript file has %d DOMContentLoaded handlers (likely duplicated append)", n)
	}
	// Trailing duplicate IIFE blocks from repeated agent turns.
	if strings.Count(text, `document.addEventListener("DOMContentLoaded"`) > 1 ||
		strings.Count(text, `document.addEventListener('DOMContentLoaded'`) > 1 {
		return fmt.Errorf("javascript file appears concatenated (multiple DOMContentLoaded blocks)")
	}
	return nil
}
