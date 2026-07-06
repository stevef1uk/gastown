package orchestrator

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CacheEntry stores a generated file's content and validation state.
type CacheEntry struct {
	Content    string `json:"content"`
	MD5        string `json:"md5"`
	Validated  bool   `json:"validated"`
	ValidatedAt string `json:"validated_at,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// CodeCache is a per-workflow cache of generated file content keyed by phase+path.
type CodeCache struct {
	mu      sync.RWMutex
	dir     string
	wfID    string
	entries map[string]*CacheEntry
}

// cacheKey builds the lookup key from phase index and relative file path.
func cacheKey(phaseIdx int, relPath string) string {
	return fmt.Sprintf("%d:%s", phaseIdx, filepath.ToSlash(relPath))
}

// OpenCodeCache opens or creates a code cache for the given workflow.
func OpenCodeCache(rigDir, workflowID string) (*CodeCache, error) {
	cacheDir := filepath.Join(rigDir, ".gastown", "code-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, err
	}
	c := &CodeCache{
		dir:     cacheDir,
		wfID:    workflowID,
		entries: map[string]*CacheEntry{},
	}
	if err := c.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return c, nil
}

func (c *CodeCache) cachePath() string {
	return filepath.Join(c.dir, c.wfID+".json")
}

func (c *CodeCache) load() error {
	data, err := os.ReadFile(c.cachePath())
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &c.entries)
}

func (c *CodeCache) save() error {
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.cachePath(), data, 0644)
}

// Put stores generated content for a phase+path.
func (c *CodeCache) Put(phaseIdx int, relPath, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := md5.Sum([]byte(content))
	key := cacheKey(phaseIdx, relPath)
	c.entries[key] = &CacheEntry{
		Content:   content,
		MD5:       fmt.Sprintf("%x", h),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	_ = c.save()
}

// MarkValidated marks an entry as having passed verification.
func (c *CodeCache) MarkValidated(phaseIdx int, relPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cacheKey(phaseIdx, relPath)
	if e, ok := c.entries[key]; ok {
		e.Validated = true
		e.ValidatedAt = time.Now().UTC().Format(time.RFC3339)
		_ = c.save()
	}
}

// GetValidated returns cached content that was previously validated for phase+path.
func (c *CodeCache) GetValidated(phaseIdx int, relPath string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := cacheKey(phaseIdx, relPath)
	e, ok := c.entries[key]
	if !ok || !e.Validated {
		return "", false
	}
	return e.Content, true
}

// GetAny returns any cached content (validated or not) for phase+path.
func (c *CodeCache) GetAny(phaseIdx int, relPath string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := cacheKey(phaseIdx, relPath)
	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	return e.Content, true
}

// ClearPhase removes all entries for a given phase index.
func (c *CodeCache) ClearPhase(phaseIdx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := fmt.Sprintf("%d:", phaseIdx)
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
	_ = c.save()
}

// InsertCachedContentIntoPrompt prepends cached validated content instructions
// into a prompt so the LLM reuses known-good code instead of regenerating.
func InsertCachedContentIntoPrompt(prompt string, phaseIdx int, relPaths []string, cache *CodeCache) string {
	var hints []string
	for _, rp := range relPaths {
		if _, ok := cache.GetValidated(phaseIdx, rp); ok {
			hints = append(hints, fmt.Sprintf("  - %s (validated — reuse existing content)", rp))
		}
	}
	if len(hints) == 0 {
		return prompt
	}
	block := "\n\n## Cached validated content available\n\nThe following files already have validated content in the code cache. " +
		"Prefer reusing them over regenerating:\n" +
		strings.Join(hints, "\n") + "\n"
	return prompt + block
}
