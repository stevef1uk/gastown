package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestProject(t *testing.T, rigDir, layout string, handlerSrc, mainSrc string) (string, string) {
	t.Helper()
	appDir := filepath.Join(rigDir, layout)
	if err := os.MkdirAll(filepath.Join(appDir, "internal", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(appDir, "cmd", "server"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "internal", "api", "handlers.go"), []byte(handlerSrc), 0644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(appDir, "cmd", "server", "main.go")
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}
	return appDir, mainPath
}

func TestHandleInlineHandlerRefactoring_detectsStub(t *testing.T) {
	dir := t.TempDir()
	layout := "linkshelf"
	rigDir := filepath.Join(dir, "mayor", "rig")

	handlerSrc := `package api

import (
	"encoding/json"
	"net/http"
)

type Handler struct{}

func (h *Handler) ListLinks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]struct{}{})
}

func (h *Handler) CreateLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(struct{}{})
}

func (h *Handler) DeleteLink(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
`
	mainSrc := `package main

import (
	"net/http"
	"linkshelf/internal/api"
)

func main() {
	h := &api.Handler{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/links", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		case http.MethodPost:
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	http.ListenAndServe(":8080", mux)
}
`
	_, mainPath := writeTestProject(t, rigDir, layout, handlerSrc, mainSrc)

	v := WorkflowValidation{
		LayoutRoot: layout,
		RequiredFiles: []string{
			"linkshelf/cmd/server/main.go",
			"linkshelf/internal/api/handlers.go",
		},
	}

	rel, err := HandleInlineHandlerRefactoring(rigDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if rel == "" {
		t.Fatal("expected auto-fix to apply")
	}

	got, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)

	if strings.Contains(body, `w.Write([]byte("[]")`) {
		t.Fatalf("inline stub handler still present after auto-fix:\n%s", body)
	}
	if !strings.Contains(body, "h.ListLinks(w, r)") {
		t.Fatalf("expected h.ListLinks(w, r) in patched main.go:\n%s", body)
	}
	if !strings.Contains(body, "h.CreateLink(w, r)") {
		t.Fatalf("expected h.CreateLink(w, r) in patched main.go:\n%s", body)
	}
}

func TestHandleInlineHandlerRefactoring_noStub(t *testing.T) {
	dir := t.TempDir()
	layout := "linkshelf"
	rigDir := filepath.Join(dir, "mayor", "rig")

	handlerSrc := `package api

import "net/http"
type Handler struct{}
func (h *Handler) ListLinks(w http.ResponseWriter, r *http.Request) {}
func (h *Handler) CreateLink(w http.ResponseWriter, r *http.Request) {}
`
	mainSrc := `package main

import (
	"net/http"
	"linkshelf/internal/api"
)

func main() {
	h := &api.Handler{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/links", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.ListLinks(w, r)
		case http.MethodPost:
			h.CreateLink(w, r)
		}
	})
	http.ListenAndServe(":8080", mux)
}
`
	_, mainPath := writeTestProject(t, rigDir, layout, handlerSrc, mainSrc)

	v := WorkflowValidation{
		LayoutRoot: layout,
		RequiredFiles: []string{
			"linkshelf/cmd/server/main.go",
		},
	}

	rel, err := HandleInlineHandlerRefactoring(rigDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "" {
		t.Fatal("expected no auto-fix when handlers are already wired")

	}
	got, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "h.ListLinks(w, r)") {
		t.Fatalf("existing delegation should remain:\n%s", string(got))
	}
}

func TestHasStubInlineHandler(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "inline stub with w.Write([]byte)",
			src:  `HandleFunc("/api/links", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) })`,
			want: true,
		},
		{
			name: "already delegating via handler variable",
			src:  `HandleFunc("/api/links", func(w http.ResponseWriter, r *http.Request) { h.ListLinks(w, r) })`,
			want: false,
		},
		{
			name: "no HandleFunc",
			src:  `func main() { http.ListenAndServe(":8080", nil) }`,
			want: false,
		},
		{
			name: "no w.Write",
			src:  `HandleFunc("/api/links", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasStubInlineHandler(tt.src)
			if got != tt.want {
				t.Errorf("hasStubInlineHandler() = %v, want %v", got, tt.want)
			}
		})
	}
}
