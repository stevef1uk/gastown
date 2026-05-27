package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// GoModuleCDDir returns the directory name to use after "cd" for Go toolchain commands,
// relative to mayor/rig. When go.mod lives at mayor/rig (flat module) but the profile still
// lists paths as layout_root/..., this returns "." instead of a missing layout subdirectory.
func GoModuleCDDir(mayorRigDir, layoutRoot string) string {
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	if mayorRigDir == "" {
		if layoutRoot == "" {
			return "."
		}
		return layoutRoot
	}
	if layoutRoot == "" || layoutRoot == "." {
		return "."
	}
	nested := filepath.Join(mayorRigDir, layoutRoot)
	if fi, err := os.Stat(filepath.Join(nested, "go.mod")); err == nil && !fi.IsDir() {
		return layoutRoot
	}
	if fi, err := os.Stat(filepath.Join(mayorRigDir, "go.mod")); err == nil && !fi.IsDir() {
		return "."
	}
	return layoutRoot
}

// implementPathCandidates returns profile-relative paths to try for stat/open.
func implementPathCandidates(relPath, layoutRoot string) []string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return nil
	}
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(relPath)
	if layoutRoot != "" {
		if strings.HasPrefix(relPath, layoutRoot+"/") {
			add(strings.TrimPrefix(relPath, layoutRoot+"/"))
		} else {
			add(layoutRoot + "/" + relPath)
		}
	}
	return out
}

// ResolveImplementRelPathOnDisk picks the candidate path that exists under mayor/rig.
// If none exist, returns the first candidate (profile-normalized path for creates).
func ResolveImplementRelPathOnDisk(mayorRigDir, relPath, layoutRoot string) string {
	candidates := implementPathCandidates(relPath, layoutRoot)
	if len(candidates) == 0 {
		return relPath
	}
	for _, c := range candidates {
		full := filepath.Join(mayorRigDir, filepath.FromSlash(c))
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			return c
		}
	}
	if GoModuleCDDir(mayorRigDir, layoutRoot) == "." {
		for _, c := range candidates {
			layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
			if layoutRoot != "" && strings.HasPrefix(c, layoutRoot+"/") {
				return strings.TrimPrefix(c, layoutRoot+"/")
			}
			if layoutRoot == "" || !strings.Contains(c, "/") {
				continue
			}
			if !strings.HasPrefix(c, layoutRoot+"/") {
				return c
			}
		}
	}
	return candidates[0]
}

// ResolveRequiredFileOnDisk returns the absolute path for a profile required_file entry.
func ResolveRequiredFileOnDisk(mayorRigDir, relPath, layoutRoot string) string {
	rel := ResolveImplementRelPathOnDisk(mayorRigDir, relPath, layoutRoot)
	return filepath.Join(mayorRigDir, filepath.FromSlash(rel))
}

// GoShellCDClause returns a leading "cd <dir> && " for verify/toolchain shell chains.
// When the module is flat at mayor/rig, returns "" so rewriteUnittestToWorkdir can prepend the full mayor/rig path.
func GoShellCDClause(mayorRigDir, layoutRoot string) string {
	dir := GoModuleCDDir(mayorRigDir, layoutRoot)
	if dir == "." {
		if mayorRigDir != "" {
			return ""
		}
		return "cd . && "
	}
	return "cd " + dir + " && "
}

// NormalizeGoLayoutPackagePaths fixes profile paths like ./linkshelf/... when the shell cwd is
// already the Go module root (mayor/rig/linkshelf) or a flat module at mayor/rig.
func NormalizeGoLayoutPackagePaths(cmd, workPath, layoutRoot string) string {
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	if layoutRoot == "" {
		return cmd
	}
	prefix := "./" + layoutRoot + "/"
	wp := strings.Trim(filepath.ToSlash(strings.TrimSpace(workPath)), "/")
	if strings.HasSuffix(wp, layoutRoot) {
		cmd = strings.ReplaceAll(cmd, "./"+layoutRoot+"/...", "./...")
		return strings.ReplaceAll(cmd, prefix, "./")
	}
	return strings.ReplaceAll(cmd, prefix, "./")
}

// GoModuleWorkPathRelative returns the shell workdir for Go commands (town-root-relative),
// e.g. testgt3/mayor/rig when the module is flat at mayor/rig instead of mayor/rig/linkshelf.
func GoModuleWorkPathRelative(mayorRigRel, layoutRoot string) string {
	mayorRigRel = strings.Trim(filepath.ToSlash(strings.TrimSpace(mayorRigRel)), "/")
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	if mayorRigRel == "" {
		return "."
	}
	if layoutRoot == "" || layoutRoot == "." {
		return mayorRigRel
	}
	nested := mayorRigRel + "/" + layoutRoot
	if fi, err := os.Stat(filepath.Join(nested, "go.mod")); err == nil && !fi.IsDir() {
		return nested
	}
	if fi, err := os.Stat(filepath.Join(mayorRigRel, "go.mod")); err == nil && !fi.IsDir() {
		return mayorRigRel
	}
	return nested
}
