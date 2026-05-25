package orchestrator

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TownAssetsSubdir is the path under townRoot where workflow templates and prompts are installed.
const TownAssetsSubdir = "orchestrator"

//go:embed town
var townAssets embed.FS

// SyncResult counts files copied during a town orchestrator sync.
type SyncResult struct {
	Added   int
	Updated int
}

// SourceDir returns the path to embedded town assets in a development checkout
// (internal/orchestrator/town). Used by make install when not using embed.
func SourceDir(gastownRoot string) string {
	return filepath.Join(gastownRoot, "internal", "orchestrator", "town")
}

// SyncTownAssets copies embedded orchestrator templates and prompts into
// {townRoot}/orchestrator/. When updateChanged is true, files that differ
// from the embedded source are overwritten (make install / gt orchestrator sync).
// When false, existing destination files are left unchanged (gt install).
func SyncTownAssets(townRoot string, updateChanged bool) (SyncResult, error) {
	return syncFromFS(townRoot, townAssets, "town", updateChanged)
}

// SyncTownAssetsFromDir copies from a filesystem directory (dev checkout).
func SyncTownAssetsFromDir(townRoot, sourceDir string, updateChanged bool) (SyncResult, error) {
	return syncFromOS(townRoot, sourceDir, updateChanged)
}

func syncFromFS(townRoot string, assets fs.FS, root string, updateChanged bool) (SyncResult, error) {
	var res SyncResult
	dstRoot := filepath.Join(townRoot, TownAssetsSubdir)
	if err := os.MkdirAll(dstRoot, 0755); err != nil {
		return res, err
	}
	err := fs.WalkDir(assets, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		added, updated, err := writeTownAsset(dstRoot, rel, data, updateChanged)
		if err != nil {
			return err
		}
		if added {
			res.Added++
		}
		if updated {
			res.Updated++
		}
		return nil
	})
	if err == nil {
		_ = InstallDefaultHTTPProfiles(townRoot)
	}
	return res, err
}

func syncFromOS(townRoot, sourceDir string, updateChanged bool) (SyncResult, error) {
	var res SyncResult
	dstRoot := filepath.Join(townRoot, TownAssetsSubdir)
	if err := os.MkdirAll(dstRoot, 0755); err != nil {
		return res, err
	}
	err := filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		added, updated, err := writeTownAsset(dstRoot, rel, data, updateChanged)
		if err != nil {
			return err
		}
		if added {
			res.Added++
		}
		if updated {
			res.Updated++
		}
		return nil
	})
	if err == nil {
		_ = InstallDefaultHTTPProfiles(townRoot)
	}
	return res, err
}

func writeTownAsset(dstRoot, rel string, data []byte, updateChanged bool) (added, updated bool, err error) {
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") {
		return false, false, fmt.Errorf("invalid asset path %q", rel)
	}
	dst := filepath.Join(dstRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return false, false, err
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return false, false, err
		}
		return true, false, nil
	} else if err != nil {
		return false, false, err
	}
	if !updateChanged {
		return false, false, nil
	}
	same, err := fileSame(dst, data)
	if err != nil {
		return false, false, err
	}
	if same {
		return false, false, nil
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return false, false, err
	}
	return false, true, nil
}

func fileSame(path string, want []byte) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		return false, err
	}
	return string(got) == string(want), nil
}

// ProvisionTownAssets installs orchestrator assets on a new town (missing files only).
func ProvisionTownAssets(townRoot string) (SyncResult, error) {
	return SyncTownAssets(townRoot, false)
}
