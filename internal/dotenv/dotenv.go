// Package dotenv loads a simple KEY=VALUE .env file for local development.
// Variables already set in the process environment are not overridden.
package dotenv

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadFile reads path and sets unset environment variables.
func LoadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if err := applyLine(scanner.Text()); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
	}
	return scanner.Err()
}

// LoadFromCwd walks up from the current directory and loads the nearest .env file.
func LoadFromCwd() (loaded string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	current := cwd
	for {
		path := filepath.Join(current, ".env")
		if _, statErr := os.Stat(path); statErr == nil {
			if err := LoadFile(path); err != nil {
				return "", err
			}
			return path, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil
		}
		current = parent
	}
}

func applyLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	key, val, ok := strings.Cut(line, "=")
	if !ok {
		return fmt.Errorf("invalid line %q", line)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("empty key")
	}
	val = strings.TrimSpace(val)
	val = strings.Trim(val, `"'`)
	val, err := expandHome(val)
	if err != nil {
		return err
	}
	if os.Getenv(key) != "" {
		return nil
	}
	return os.Setenv(key, val)
}

func expandHome(path string) (string, error) {
	if path == "" || !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[2:]), nil
}
