package cmd

import "testing"

func TestCommandNeedsPTY(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"gt-agent", "--orchestrated"}, false},
		{[]string{"bash", "-c", "exec env GT_ROLE=qa /usr/bin/gt-agent --orchestrated"}, false},
		{[]string{"claude", "--dangerously-skip-permissions"}, true},
		{[]string{"gt-agent"}, true},
		{nil, true},
	}
	for _, tc := range cases {
		if got := commandNeedsPTY(tc.args); got != tc.want {
			t.Errorf("commandNeedsPTY(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
