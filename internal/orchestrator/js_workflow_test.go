package orchestrator

import "testing"

func TestNodeProjectSetupVerifyCommand(t *testing.T) {
	cases := []struct {
		name string
		v    WorkflowValidation
		want string
	}{
		{
			name: "frontend subdirectory",
			v:    WorkflowValidation{QAVerifyCommand: "cd frontend && npm test"},
			want: "cd frontend && npm install",
		},
		{
			name: "root level npm",
			v:    WorkflowValidation{QAVerifyCommand: "npm test"},
			want: "npm install",
		},
		{
			name: "app layout",
			v:    WorkflowValidation{QAVerifyCommand: "cd app && npm test"},
			want: "cd app && npm install",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NodeProjectSetupVerifyCommand(tc.v)
			if got != tc.want {
				t.Errorf("NodeProjectSetupVerifyCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}
