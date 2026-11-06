package deps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseBeadsVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"bd version 0.55.4 (dev: main@3e1378e122c6)", "0.55.4"},
		{"bd version 0.55.4", "0.55.4"},
		{"bd version 1.2.3", "1.2.3"},
		{"bd version 10.20.30 (release)", "10.20.30"},
		{"some other output", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := parseBeadsVersion(tt.input)
		if result != tt.expected {
			t.Errorf("parseBeadsVersion(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"0.55.4", "0.55.4", 0},
		{"0.55.4", "0.54.0", 1},
		{"0.54.0", "0.55.4", -1},
		{"1.0.0", "0.99.99", 1},
		{"0.55.5", "0.55.4", 1},
		{"0.55.4", "0.55.5", -1},
	}

	for _, tt := range tests {
		result := CompareVersions(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestCheckBeads(t *testing.T) {
	// This test depends on whether bd is installed in the test environment
	status, version := CheckBeads()

	// We expect bd to be installed in dev environment
	if status == BeadsNotFound {
		t.Skip("bd not installed, skipping integration test")
	}

	if status == BeadsOK && version == "" {
		t.Error("CheckBeads returned BeadsOK but empty version")
	}

	t.Logf("CheckBeads: status=%d, version=%s", status, version)
}

func TestBeadsCLIPinMatchesSDK(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "github.com/steveyegge/beads v"+BeadsVersion+"\n") {
		t.Fatal("Beads CLI pin must match go.mod SDK version")
	}
	for _, name := range []string{"Dockerfile", "Dockerfile.e2e"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "ARG BD_VERSION=v"+BeadsVersion+"\n") {
			t.Errorf("%s Beads pin does not match SDK", name)
		}
	}
	if strings.Contains(BeadsInstallPath, "@latest") {
		t.Fatal("dependency installation must be pinned")
	}
}

func TestBeadsVersionCompatibility(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, tc := range []struct {
		version string
		want    BeadsStatus
	}{
		{BeadsVersion, BeadsOK}, {"1.0.0", BeadsTooOld}, {"1.3.2", BeadsTooOld}, {"99.0.0", BeadsIncompatible}, {"unknown", BeadsUnknown},
	} {
		t.Run(tc.version, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "bd"), []byte("#!/bin/sh\necho 'bd version "+tc.version+"'\n"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			status, _ := CheckBeads()
			if status != tc.want {
				t.Fatalf("got %v want %v", status, tc.want)
			}
			if err := EnsureBeads(false); (err == nil) != (tc.want == BeadsOK) {
				t.Fatalf("unexpected compatibility result: %v", err)
			}
		})
	}
}

func TestEnsureBeadsInstallsPinnedCLIForFreshTown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, initial := range []string{"", "1.0.0", "99.0.0"} {
		t.Run("initial="+initial, func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(bin, 0755); err != nil {
				t.Fatal(err)
			}
			if initial != "" {
				if err := os.WriteFile(filepath.Join(bin, "bd"), []byte("#!/bin/sh\necho 'bd version "+initial+"'\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			script := "#!/bin/sh\n" +
				"[ \"$1\" = install ] && [ \"$2\" = -tags=gms_pure_go ] && [ \"$3\" = " + BeadsInstallPath + " ] || exit 42\n" +
				"printf '#!/bin/sh\\necho \"bd version " + BeadsVersion + "\"\\n' > \"$GOBIN/bd\"\n/bin/chmod +x \"$GOBIN/bd\"\n"
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("PATH", bin)
			if err := EnsureBeads(true); err != nil {
				t.Fatal(err)
			}
			if status, version := CheckBeads(); status != BeadsOK || version != BeadsVersion {
				t.Fatalf("selected CLI after install: %v %s", status, version)
			}
		})
	}
}
