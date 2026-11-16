package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Match command positions, not a grep/read argument mentioning these tools.
// Managed child-PID cleanup (pkill -P) remains available to the server wrapper;
// matching all processes by name/command line or killing by port is not QA work.
var qaProcessCleanupRE = regexp.MustCompile(`(?im)(?:^|[;&|\n(])[\t ]*(?:(?:sudo|command|exec)[\t ]+)*(?:[^\s;&|()]+/)?(pkill|killall|fuser)(?:[\t ]+([^;&|\n]*))?(?:$|[;&|\n])`)

func rejectBroadQAProcessCleanup(cmd string) error {
	for _, match := range qaProcessCleanupRE.FindAllStringSubmatch(cmd, -1) {
		tool, args := strings.ToLower(filepath.Base(match[1])), strings.Fields(match[2])
		broad := tool == "killall" || tool == "pkill"
		if tool == "pkill" {
			childOnly, fullMatch := false, false
			for _, arg := range args {
				childOnly = childOnly || arg == "-P" || strings.HasPrefix(arg, "--parent")
				fullMatch = fullMatch || arg == "--full" || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "f"))
			}
			broad = !childOnly || fullMatch
		}
		if tool == "fuser" {
			for _, arg := range args {
				broad = broad || arg == "--kill" || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "k"))
			}
		}
		if broad {
			return fmt.Errorf("QA must not use broad process cleanup (%s); run the configured phase verification directly. Use verification_error for command/environment problems, not implementation failure", tool)
		}
	}
	return nil
}
