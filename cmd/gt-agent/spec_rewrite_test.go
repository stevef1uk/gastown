package main

import "testing"

func TestRewriteSPECMDPathAfterCD(t *testing.T) {
	cases := []struct {
		cmd, want string
		ok        bool
	}{
		{"cd fin/mayor/rig && cat fin/mayor/rig/SPEC.md", "cd fin/mayor/rig && cat SPEC.md", true},
		{"cd fin/mayor/rig && head -240 fin/mayor/rig/SPEC.md", "cd fin/mayor/rig && head -240 SPEC.md", true},
		{"cd fin/mayor/rig && cat SPEC.md", "cd fin/mayor/rig && cat SPEC.md", false},
		{"cat fin/mayor/rig/SPEC.md", "cat fin/mayor/rig/SPEC.md", false},
		{"cd fin/mayor/rig && cat fin/mayor/rig/architecture.md", "cd fin/mayor/rig && cat fin/mayor/rig/architecture.md", false},
	}
	for _, c := range cases {
		got, ok := rewriteSPECMDPathAfterCD(c.cmd, "fin")
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("rewriteSPECMDPathAfterCD(%q) = (%q, %v), want (%q, %v)", c.cmd, got, ok, c.want, c.ok)
		}
	}
}
