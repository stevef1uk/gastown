package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectWeakTestAssertions_playwrightClean(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "test/e2e.spec.ts", `
import { test, expect } from "@playwright/test";
test("dashboard renders watchlist", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Market Dashboard" })).toBeVisible();
  await expect(page.getByText("AAPL")).toBeVisible();
  await expect(page.locator(".watchlist-list")).toContainText("$10,000");
});
`)
	report := DetectWeakTestAssertions(dir)
	if !report.IsClean() {
		t.Fatalf("expected clean, got: %v", report.Issues)
	}
}

func TestDetectWeakTestAssertions_statusOnly(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "test/e2e.spec.ts", `
import { test, expect } from "@playwright/test";
test("health and dashboard are available", async ({ page }) => {
  await page.goto(baseURL);
  const response = await page.goto(baseURL, { waitUntil: "domcontentloaded" });
  expect(response?.status()).toBe(200);
  expect(response?.headers()["content-type"]).toContain("text/html");
  await expect(page.locator("body")).toBeVisible();
});
`)
	report := DetectWeakTestAssertions(dir)
	if report.IsClean() {
		t.Fatal("expected weak-assertion issue")
	}
	found := false
	for _, issue := range report.Issues {
		if strings.Contains(issue, "does not assert real content") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected weak-assertion issue text, got: %v", report.Issues)
	}
}

func TestDetectWeakTestAssertions_nestedLayout(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "finally/test/e2e.spec.ts", `
import { test, expect } from "@playwright/test";
test("status only", async ({ page }) => {
  const response = await page.goto("/");
  expect(response?.status()).toBe(200);
});
`)
	report := DetectWeakTestAssertions(dir)
	if report.IsClean() {
		t.Fatal("expected weak-assertion issue for nested spec")
	}
	if len(report.Issues) != 1 {
		t.Fatalf("expected exactly one issue, got: %v", report.Issues)
	}
}

func TestDetectWeakTestAssertions_commentOnlyContent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "test/e2e.spec.ts", `
import { test, expect } from "@playwright/test";
// await expect(page.getByText("AAPL")).toBeVisible();
test("status only", async ({ page }) => {
  const response = await page.goto("/");
  expect(response?.status()).toBe(200);
});
`)
	report := DetectWeakTestAssertions(dir)
	if report.IsClean() {
		t.Fatal("expected weak-assertion issue; commented-out content assertion should not count")
	}
}

func TestDetectWeakTestAssertions_pytestClean(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "backend/tests/test_main.py", `
def test_watchlist(client):
    resp = client.get("/api/watchlist")
    assert resp.status_code == 200
    data = resp.json()
    assert data["items"][0]["ticker"] == "AAPL"
`)
	report := DetectWeakTestAssertions(dir)
	if !report.IsClean() {
		t.Fatalf("expected clean for pytest with content assertions, got: %v", report.Issues)
	}
}

func TestDetectWeakTestAssertions_pytestStatusOnly(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "backend/tests/test_main.py", `
def test_health(client):
    resp = client.get("/api/health")
    assert resp.status_code == 200
`)
	report := DetectWeakTestAssertions(dir)
	if report.IsClean() {
		t.Fatal("expected weak-assertion issue for pytest status-only")
	}
}

func TestDetectWeakTestAssertions_pytestEnshrinesPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "backend/tests/test_main.py", `
def test_dashboard_fallback(client):
    dashboard = client.get("/")
    assert dashboard.status_code == 200
    assert "Finally" in dashboard.text
`)
	report := DetectWeakTestAssertions(dir)
	if report.IsClean() {
		t.Fatal("expected placeholder-enshrining issue")
	}
	found := false
	for _, issue := range report.Issues {
		if strings.Contains(issue, "placeholder/fallback text") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected placeholder issue text, got: %v", report.Issues)
	}
}

func TestDetectWeakTestAssertions_goClean(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "backend/api_test.go", `
func TestHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/health", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if got, want := rec.Body.String(), `+"`"+`{"status":"ok"}`+"`"+`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
`)
	report := DetectWeakTestAssertions(dir)
	if !report.IsClean() {
		t.Fatalf("expected clean for Go test with body assertion, got: %v", report.Issues)
	}
}

func TestDetectWeakTestAssertions_goStatusOnly(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "backend/api_test.go", `
func TestHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/health", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
}
`)
	report := DetectWeakTestAssertions(dir)
	if report.IsClean() {
		t.Fatal("expected weak-assertion issue for Go status-only test")
	}
}