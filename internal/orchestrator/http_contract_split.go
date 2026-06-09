package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateHTTPContractSplit returns blocking issues (must fix before bd close) and warnings
// (cross-bead refs still on open implement beads, or assets present under alternate paths).
func ValidateHTTPContractSplit(townRoot, rig string, v WorkflowValidation) (blocking, warnings []string, err error) {
	if !WorkflowNeedsRuntimeSmoke(townRoot, rig, v) {
		return nil, nil, nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	contract := HTTPContractFromRig(townRoot, rig, v)
	openPaths := openImplementPathSet(townRoot, rig, v)
	b, w := validateWebHTMLContractSplit(townRoot, rig, rigDir, v, contract.Static, openPaths)
	blocking = append(blocking, b...)
	warnings = append(warnings, w...)
	if path := firstHandlerImplementPath(v); path != "" {
		body, readErr := os.ReadFile(filepath.Join(rigDir, filepath.FromSlash(path)))
		if readErr == nil {
			blocking = append(blocking, handlerContractIssues(townRoot, rig, string(body), v)...)
		}
	}
	return blocking, warnings, nil
}

// ValidateHTTPContract checks HTML refs and handler source against architecture static routing.
func ValidateHTTPContract(townRoot, rig string, v WorkflowValidation) error {
	blocking, _, err := ValidateHTTPContractSplit(townRoot, rig, v)
	if err != nil {
		return err
	}
	if len(blocking) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(blocking, "; "))
}

func validateWebHTMLContract(rigDir string, v WorkflowValidation, mapping WebStaticMapping) []string {
	blocking, _ := validateWebHTMLContractSplit("", "", rigDir, v, mapping, nil)
	return blocking
}

func validateWebHTMLContractSplit(townRoot, rig, rigDir string, v WorkflowValidation, mapping WebStaticMapping, openPaths map[string]bool) (blocking, warnings []string) {
	for _, htmlRel := range webHTMLPaths(v) {
		abs := filepath.Join(rigDir, filepath.FromSlash(htmlRel))
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		webRoot := webRootDir(rigDir, htmlRel, v)
		for _, m := range htmlAttrRefContractRE.FindAllStringSubmatch(string(body), -1) {
			if len(m) < 3 {
				continue
			}
			ref := strings.TrimSpace(m[2])
			if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "http") || strings.HasPrefix(ref, "/api/") {
				continue
			}
			lower := strings.ToLower(ref)
			if !strings.HasSuffix(lower, ".js") && !strings.HasSuffix(lower, ".css") {
				continue
			}
			if hint := mapping.StaticRefMismatchHint(ref); hint != "" {
				blocking = append(blocking, fmt.Sprintf("%s: %s", htmlRel, hint))
				continue
			}
			if staticRefExistsOnDisk(rigDir, v, mapping, webRoot, htmlRel, ref) {
				continue
			}
			msg := fmt.Sprintf("%s references %q but %s is missing under web/", htmlRel, ref, staticRefBaseName(ref, mapping))
			layoutPath := layoutWebPathForStaticRef(v, ref, mapping)
			if layoutPath != "" && openPaths != nil && openPaths[layoutPath] {
				warnings = append(warnings, msg+" (open implement bead — finish sibling assets first)")
				continue
			}
			if layoutPath != "" && openPaths != nil {
				for openPath := range openPaths {
					if openPath == layoutPath || strings.HasSuffix(openPath, "/"+filepath.Base(layoutPath)) {
						warnings = append(warnings, msg+" (open implement bead — finish sibling assets first)")
						goto nextRef
					}
				}
			}
			blocking = append(blocking, msg)
		nextRef:
		}
	}
	return blocking, warnings
}

func openImplementPathSet(townRoot, rig string, v WorkflowValidation) map[string]bool {
	if townRoot == "" || rig == "" {
		return nil
	}
	beads, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	titlePrefix := v.BeadTitleContains
	if strings.TrimSpace(titlePrefix) == "" {
		titlePrefix = "Implement "
	}
	for _, b := range beads {
		if !MatchesImplementBeadTitle(b.Title, v) {
			continue
		}
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, titlePrefix), v.LayoutRoot, rig)
		if p != "" {
			out[p] = true
		}
	}
	return out
}

func layoutWebPathForStaticRef(v WorkflowValidation, ref string, mapping WebStaticMapping) string {
	base := staticRefBaseName(ref, mapping)
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if base == "" || layout == "" {
		return ""
	}
	return filepath.ToSlash(layout + "/web/" + base)
}

func staticRefBaseName(ref string, mapping WebStaticMapping) string {
	ref = normalizeWebURLRef(ref)
	if ref == "" {
		return ""
	}
	if mapping.StaticURLPrefix != "" && strings.HasPrefix(ref, mapping.StaticURLPrefix+"/") {
		return filepath.Base(strings.TrimPrefix(ref, mapping.StaticURLPrefix+"/"))
	}
	if strings.HasPrefix(ref, "/") {
		return filepath.Base(ref)
	}
	return filepath.Base(ref)
}

func staticRefExistsOnDisk(rigDir string, v WorkflowValidation, mapping WebStaticMapping, webRoot, htmlRel, ref string) bool {
	disk := mapping.WebDiskPathForURLRef(webRoot, htmlRel, ref)
	if disk != "" {
		if info, err := os.Stat(disk); err == nil && !info.IsDir() {
			return true
		}
	}
	layoutPath := layoutWebPathForStaticRef(v, ref, mapping)
	if layoutPath != "" {
		alt := filepath.Join(rigDir, filepath.FromSlash(layoutPath))
		if info, err := os.Stat(alt); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
