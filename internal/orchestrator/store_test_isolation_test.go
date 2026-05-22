package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorePackageTestIsolationHint_sharedDBFailure(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	storeDir := filepath.Join(layout, "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	storeSrc := `package store

func OpenDB() (*Store, error) {
	db, err := sql.Open("sqlite3", "./links.db")
	return &Store{db: db}, err
}
`
	testSrc := `package store

func setupTestStore(t *testing.T) (*Store, func()) {
	dbFile := t.TempDir() + "/test.db"
	store, err := OpenDB()
	return store, func() {}
}
`
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte(storeSrc), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte(testSrc), 0644); err != nil {
		t.Fatal(err)
	}
	out := `--- FAIL: TestAddBookmark (0.00s)
    store_test.go:41: expected 1 bookmark, got 69
`
	hint := StorePackageTestIsolationHint(dir, "linkshelf", out)
	if hint == "" {
		t.Fatal("expected hint")
	}
	if !strings.Contains(hint, "fresh") {
		t.Fatalf("hint should mention fresh DB: %q", hint)
	}
}

func TestStorePackageTestIsolationHint_storeUsesSharedDBOnDisk(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "store.go"), []byte("package store\nfunc OpenDB() { sql.Open(\"sqlite3\", \"./links.db\") }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "store_test.go"), []byte("package store\nfunc setup(t *testing.T) { _ = t.TempDir(); OpenDB() }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Small mismatch — still hint because on-disk pattern matches shared DB smell.
	out := `FAIL	linkshelf/internal/store [build failed]`
	hint := StorePackageTestIsolationHint(dir, "linkshelf", out)
	if hint == "" {
		t.Fatal("expected hint from on-disk OpenDB()+TempDir pattern")
	}
}

func TestStorePackageTestIsolationHint_isolatedMemoryNoHint(t *testing.T) {
	out := `--- FAIL: TestCreateBookmark (0.00s)
    store_test.go:68: expected 1 bookmark, got 2
`
	hint := StorePackageTestIsolationHint(t.TempDir(), "linkshelf", out)
	if hint != "" {
		t.Fatalf("unexpected hint for small count mismatch: %q", hint)
	}
}
