package orchestrator

import "testing"

func TestIsTestImplementPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		{"linkshelf/internal/store/store_test.go", true},
		{"linkshelf/internal/store/store.go", false},
		{"backend/tests/test_app.py", true},
		{"backend/app.py", false},
	}
	for _, tc := range cases {
		if got := IsTestImplementPath(tc.path); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.path, got, tc.want)
		}
	}
}

func TestCorrelatedTestPathForSource_go(t *testing.T) {
	t.Parallel()
	got := CorrelatedTestPathForSource("linkshelf/internal/store/store.go", WorkflowValidation{LayoutRoot: "linkshelf"})
	if got != "linkshelf/internal/store/store_test.go" {
		t.Fatalf("got %q", got)
	}
}

func TestCorrelatedTestPathForSource_python_prefersRequiredTest(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    "finally",
		RequiredFiles: []string{"finally/backend/tests/test_main.py"},
	}
	got := CorrelatedTestPathForSource("finally/backend/app/main.py", v)
	if got != "finally/backend/tests/test_main.py" {
		t.Fatalf("got %q", got)
	}
}

func TestGoCompileVerifyCommandForBead_runsPackageTests(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/store_test.go"},
	}
	dir := t.TempDir()
	wantTest := "cd linkshelf && go mod tidy && go test -timeout 30s -count=1 ./internal/store/..."
	gotTest := GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/store_test.go")
	if gotTest != wantTest {
		t.Fatalf("test bead got %q", gotTest)
	}
}
