package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodeCache_putAndGet(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCodeCache(dir, "wf-test")
	if err != nil {
		t.Fatal(err)
	}

	c.Put(1, "cmd/server/main.go", "package main\nfunc main() {}\n")

	got, ok := c.GetAny(1, "cmd/server/main.go")
	if !ok {
		t.Fatal("expected cached entry")
	}
	if got != "package main\nfunc main() {}\n" {
		t.Fatalf("got %q", got)
	}
}

func TestCodeCache_validatedFlag(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCodeCache(dir, "wf-test2")
	if err != nil {
		t.Fatal(err)
	}

	c.Put(0, "store.go", "package store")
	_, ok := c.GetValidated(0, "store.go")
	if ok {
		t.Fatal("should not be validated yet")
	}

	c.MarkValidated(0, "store.go")
	got, ok := c.GetValidated(0, "store.go")
	if !ok {
		t.Fatal("expected validated entry")
	}
	if got != "package store" {
		t.Fatalf("got %q", got)
	}
}

func TestCodeCache_persistence(t *testing.T) {
	dir := t.TempDir()

	c1, err := OpenCodeCache(dir, "wf-persist")
	if err != nil {
		t.Fatal(err)
	}
	c1.Put(2, "handlers.go", "package api")
	c1.MarkValidated(2, "handlers.go")

	// Re-open same workflow — should load saved entries
	c2, err := OpenCodeCache(dir, "wf-persist")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c2.GetValidated(2, "handlers.go")
	if !ok {
		t.Fatal("expected persisted validated entry")
	}
	if got != "package api" {
		t.Fatalf("got %q", got)
	}
}

func TestCodeCache_invalidatesWhenDesignChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte("version 1"), 0644); err != nil {
		t.Fatal(err)
	}
	c1, err := OpenCodeCache(dir, "wf-design")
	if err != nil {
		t.Fatal(err)
	}
	c1.Put(0, "Dockerfile", "FROM node:20")
	c1.MarkValidated(0, "Dockerfile")

	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte("version 2"), 0644); err != nil {
		t.Fatal(err)
	}
	c2, err := OpenCodeCache(dir, "wf-design")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c2.GetValidated(0, "Dockerfile"); ok {
		t.Fatal("expected cached entry to be invalidated when architecture changes")
	}
}

func TestCodeCache_invalidatesLegacyEntriesForDesignedRigs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("spec"), 0644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, ".gastown", "code-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"0:stale.go":{"content":"old","md5":"x","validated":true,"created_at":"2026-01-01T00:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(cacheDir, "wf-legacy.json"), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCodeCache(dir, "wf-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.GetValidated(0, "stale.go"); ok {
		t.Fatal("expected legacy cache entry to be invalidated for designed rig")
	}
}

func TestCodeCache_clearPhase(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCodeCache(dir, "wf-clear")
	if err != nil {
		t.Fatal(err)
	}

	c.Put(0, "file1.go", "a")
	c.Put(0, "file2.go", "b")
	c.Put(1, "file3.go", "c")

	c.ClearPhase(0)

	_, ok := c.GetAny(0, "file1.go")
	if ok {
		t.Fatal("expected phase 0 entries to be cleared")
	}
	_, ok = c.GetAny(0, "file2.go")
	if ok {
		t.Fatal("expected phase 0 entries to be cleared")
	}
	got, ok := c.GetAny(1, "file3.go")
	if !ok {
		t.Fatal("expected phase 1 entries to remain")
	}
	if got != "c" {
		t.Fatalf("got %q", got)
	}
}

func TestCodeCache_insertIntoPrompt(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCodeCache(dir, "wf-prompt")
	if err != nil {
		t.Fatal(err)
	}

	c.Put(0, "store.go", "package store")
	c.MarkValidated(0, "store.go")

	prompt := "Write the handler code."
	result := InsertCachedContentIntoPrompt(prompt, 0, []string{"store.go"}, c)
	if result == prompt {
		t.Fatal("expected prompt to be augmented with cache hint")
	}
	if !cacheContains(result, "store.go") || !cacheContains(result, "validated") {
		t.Fatalf("cache hint missing from prompt:\n%s", result)
	}

	// Non-validated file should not appear
	c.Put(0, "handlers.go", "package api")
	result2 := InsertCachedContentIntoPrompt(prompt, 0, []string{"store.go", "handlers.go"}, c)
	if !cacheContains(result2, "store.go") {
		t.Fatal("expected store.go hint")
	}
	if cacheContains(result2, "handlers.go") {
		t.Fatal("handlers.go is not validated, should not appear")
	}
}

func cacheContains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && s != substr &&
		len(s) >= len(substr) &&
		(s == substr ||
			len(s) > len(substr) &&
				(s[:len(substr)] == substr ||
					s[len(s)-len(substr):] == substr ||
					cacheFindInMiddle(s, substr)))
}

func cacheFindInMiddle(s, substr string) bool {
	for i := 1; i < len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
