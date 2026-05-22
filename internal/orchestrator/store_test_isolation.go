package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var storeTestExpectedGotRE = regexp.MustCompile(`expected (\d+)[^,\n]*, got (\d+)`)

// StorePackageTestIsolationHint returns LLM feedback when store package tests likely
// share a persistent SQLite file instead of a fresh DB per test.
func StorePackageTestIsolationHint(mayorRigDir, layoutRoot, testOutput string) string {
	testOutput = strings.TrimSpace(testOutput)
	if testOutput == "" {
		return ""
	}
	storeRel := filepath.ToSlash(filepath.Join(strings.TrimSpace(layoutRoot), "internal/store"))
	if !strings.Contains(testOutput, "store_test.go") && !strings.Contains(testOutput, storeRel) {
		return ""
	}
	if !looksLikeSharedDBTestFailure(testOutput) && !storePackageUsesSharedDBPath(mayorRigDir, layoutRoot) {
		return ""
	}
	return strings.TrimSpace("### SQLite test isolation (required)\n" +
		"Store tests are failing because they read **leftover rows** from a **shared on-disk DB** (e.g. `./links.db`), not a clean database.\n\n" +
		"Before asserting counts, each test must use a **fresh** database:\n" +
		"- Change `OpenDB` to accept a **db path** argument (production may default to `./links.db` or `linkshelf.db`).\n" +
		"- In `*_test.go`, open with `filepath.Join(t.TempDir(), \"test.db\")` or `sql.Open(\"sqlite3\", \":memory:\")`, then call `InitSchema(db)` before any queries.\n" +
		"- Do **not** call `OpenDB()` with no path while creating an unused `t.TempDir()` file — the helper and tests must use the **same** path.\n" +
		"- Run `InitSchema` on every new connection; never assume an empty `links` table on a reused file.\n\n" +
		"Re-run **Verify** after fixing `store.go` and `store_test.go`.")
}

func looksLikeSharedDBTestFailure(out string) bool {
	for _, m := range storeTestExpectedGotRE.FindAllStringSubmatch(out, -1) {
		if len(m) < 3 {
			continue
		}
		exp, err1 := strconv.Atoi(m[1])
		got, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil {
			continue
		}
		if got > exp+2 || (exp <= 1 && got > 5) {
			return true
		}
	}
	return false
}

func storePackageUsesSharedDBPath(mayorRigDir, layoutRoot string) bool {
	dir := filepath.Join(mayorRigDir, strings.TrimSpace(layoutRoot), "internal", "store")
	storeGo, err := os.ReadFile(filepath.Join(dir, "store.go"))
	if err != nil {
		return false
	}
	s := string(storeGo)
	if strings.Contains(s, `func OpenDB()`) {
		return strings.Contains(s, `links.db`) || strings.Contains(s, `linkshelf.db`)
	}
	testGo, err := os.ReadFile(filepath.Join(dir, "store_test.go"))
	if err != nil {
		return false
	}
	ts := string(testGo)
	return strings.Contains(ts, `OpenDB()`) && (strings.Contains(ts, `t.TempDir()`) || strings.Contains(ts, `test.db`))
}
