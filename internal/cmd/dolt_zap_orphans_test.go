package cmd

import (
	"testing"
)

// TestClassifyDoltProc covers every branch of the decision function.
// classifyDoltProc is the pure-data part of the zap-orphans command —
// it must NEVER kill the canonical server, must NEVER kill an active
// bd/gt child, and must always kill anything reparented to init.
func TestClassifyDoltProc(t *testing.T) {
	tests := []struct {
		name          string
		proc          doltProcInfo
		canonicalPID  int
		canonicalPort int
		want          doltProcClassification
	}{
		{
			name:          "exact canonical pid match wins above all",
			proc:          doltProcInfo{PID: 1234, PPID: 1, Port: 9999, ParentCmd: ""},
			canonicalPID:  1234,
			canonicalPort: 3307,
			want:          classPreserveCanonicalPID,
		},
		{
			name:          "canonical port match preserves even if pid is unknown",
			proc:          doltProcInfo{PID: 1234, PPID: 1, Port: 3307, ParentCmd: ""},
			canonicalPID:  0, // state.json missing / stale
			canonicalPort: 3307,
			want:          classPreserveCanonicalPort,
		},
		{
			name:          "active bd parent preserves an ephemeral server",
			proc:          doltProcInfo{PID: 4242, PPID: 4200, Port: 39711, ParentCmd: "bd"},
			canonicalPID:  9999,
			canonicalPort: 3307,
			want:          classPreserveActiveParent,
		},
		{
			name:          "active gt parent preserves",
			proc:          doltProcInfo{PID: 4242, PPID: 4200, Port: 39711, ParentCmd: "gt"},
			canonicalPID:  0,
			canonicalPort: 3307,
			want:          classPreserveActiveParent,
		},
		{
			name:          "active gt-agent parent preserves",
			proc:          doltProcInfo{PID: 4242, PPID: 4200, Port: 39711, ParentCmd: "gt-agent"},
			canonicalPID:  0,
			canonicalPort: 3307,
			want:          classPreserveActiveParent,
		},
		{
			name:          "reparented to init (ppid=1, parent_cmd=systemd or empty) is orphan",
			proc:          doltProcInfo{PID: 4242, PPID: 1, Port: 39711, ParentCmd: "systemd"},
			canonicalPID:  9999,
			canonicalPort: 3307,
			want:          classOrphan,
		},
		{
			name:          "parent gone (ParentCmd empty) is orphan",
			proc:          doltProcInfo{PID: 4242, PPID: 99999, Port: 39711, ParentCmd: ""},
			canonicalPID:  9999,
			canonicalPort: 3307,
			want:          classOrphan,
		},
		{
			name:          "live parent that is NOT on the allowlist is orphan (defensive)",
			proc:          doltProcInfo{PID: 4242, PPID: 4200, Port: 39711, ParentCmd: "bash"},
			canonicalPID:  9999,
			canonicalPort: 3307,
			want:          classOrphan,
		},
		{
			name:          "live parent named like a normal user shell is orphan",
			proc:          doltProcInfo{PID: 4242, PPID: 4200, Port: 39711, ParentCmd: "tmux"},
			canonicalPID:  9999,
			canonicalPort: 3307,
			want:          classOrphan,
		},
		{
			name:          "canonical preserve wins over allowlisted parent",
			proc:          doltProcInfo{PID: 1234, PPID: 4200, Port: 3307, ParentCmd: "bd"},
			canonicalPID:  1234,
			canonicalPort: 3307,
			want:          classPreserveCanonicalPID,
		},
		{
			name:          "port preserve wins over orphan-by-parent classification",
			proc:          doltProcInfo{PID: 4242, PPID: 1, Port: 3307, ParentCmd: ""},
			canonicalPID:  0,
			canonicalPort: 3307,
			want:          classPreserveCanonicalPort,
		},
		{
			name:          "port=0 with no parent allowlist match is orphan",
			proc:          doltProcInfo{PID: 4242, PPID: 1, Port: 0, ParentCmd: ""},
			canonicalPID:  0,
			canonicalPort: 3307,
			want:          classOrphan,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDoltProc(tc.proc, tc.canonicalPID, tc.canonicalPort)
			if got != tc.want {
				t.Errorf("classifyDoltProc(%+v, pid=%d, port=%d) = %s, want %s",
					tc.proc, tc.canonicalPID, tc.canonicalPort, got, tc.want)
			}
		})
	}
}

// TestParsePortFromCmdline covers the `-P` / `--port` parser. The dolt
// binary's argv layout varies between distros / shells, so we accept
// both flag forms and a missing flag.
func TestParsePortFromCmdline(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    int
	}{
		{
			name:    "no port flag returns 0",
			cmdline: "/usr/bin/dolt sql-server -H 127.0.0.1",
			want:    0,
		},
		{
			name:    "short -P flag",
			cmdline: "/usr/bin/dolt sql-server -H 127.0.0.1 -P 3307",
			want:    3307,
		},
		{
			name:    "short -P with leading spaces survives Fields()",
			cmdline: "   /usr/bin/dolt   sql-server    -H 127.0.0.1   -P 39711   ",
			want:    39711,
		},
		{
			name:    "long --port flag",
			cmdline: "/usr/bin/dolt sql-server --port 3307",
			want:    3307,
		},
		{
			name:    "garbage port number returns 0 without panic",
			cmdline: "/usr/bin/dolt sql-server -P notanint",
			want:    0,
		},
		{
			name:    "trailing -P with no value returns 0",
			cmdline: "/usr/bin/dolt sql-server -P",
			want:    0,
		},
		{
			name:    "literal -PX (no space) does NOT match short flag",
			cmdline: "/usr/bin/dolt sql-server -P3307",
			want:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePortFromCmdline(tc.cmdline)
			if got != tc.want {
				t.Errorf("parsePortFromCmdline(%q) = %d, want %d", tc.cmdline, got, tc.want)
			}
		})
	}
}
