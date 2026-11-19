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

func rejectPlaywrightSystemDependencyInstall(cmd string) error {
	for _, segment := range strings.FieldsFunc(strings.ToLower(cmd), func(r rune) bool {
		return r == ';' || r == '\n' || r == '|'
	}) {
		if i := strings.LastIndex(segment, "&&"); i >= 0 {
			segment = segment[i+2:]
		}
		fields := strings.Fields(segment)
		start := 0
		if len(fields) > 0 {
			switch strings.Trim(fields[0], `'"`) {
			case "npx", "yarn":
				start = 1
			case "npm", "pnpm":
				if len(fields) > 1 && strings.Trim(fields[1], `'"`) == "exec" {
					start = 2
				}
			}
		}
		if len(fields) <= start+2 ||
			strings.Trim(fields[start], `'"`) != "playwright" ||
			strings.Trim(fields[start+1], `'"`) != "install" {
			continue
		}
		for _, field := range fields[start+2:] {
			if strings.Trim(field, `'"`) == "--with-deps" {
				return fmt.Errorf("QA must not run Playwright with --with-deps because it invokes OS package installation; use the configured Playwright test command without installing system dependencies")
			}
		}
	}
	return nil
}
