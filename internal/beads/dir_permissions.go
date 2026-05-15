package beads

import (
	"fmt"
	"os"
)

// EnsureBeadsDirMode0700 sets an existing .beads directory to mode 0700.
// Dolt and bd expect a private beads directory; umask or prior mkdir can leave 0755.
// No-op when beadsDir does not exist.
func EnsureBeadsDirMode0700(beadsDir string) error {
	if beadsDir == "" {
		return nil
	}
	info, err := os.Stat(beadsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", beadsDir)
	}
	return os.Chmod(beadsDir, 0700)
}
