package session

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/gofrs/flock"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Serialize session lifecycle across independent CLI/daemon/orchestrator
// processes. Never unlink a lock file: all contenders must lock the same inode.
func lockNatsSession(ctx context.Context, townRoot, sessionID string) (*flock.Flock, error) {
	dir := filepath.Join(townRoot, ".runtime", "session-locks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(sessionID))
	lock := flock.New(filepath.Join(dir, fmt.Sprintf("%x.lock", key)))
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	acquired, err := lock.TryLockContext(bounded, 25*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("session lifecycle busy: %s", sessionID)
	}
	return lock, nil
}

func removeNatsSessionPIDIfOwned(townRoot, sessionID string, pid int) {
	lock, err := lockNatsSession(context.Background(), townRoot, sessionID)
	if err != nil {
		return
	}
	defer lock.Unlock()
	path := filepath.Join(townRoot, ".gt-nats-pids", sessionID)
	data, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(pid) {
		_ = os.Remove(path)
	}
}
