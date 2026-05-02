package main

import (
	"fmt"
	"os/exec"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

func main() {
	townRoot := "/home/stevef/dev/freeride/gt"
	fmt.Printf("Town Root: %s\n", townRoot)

	// Initialize registry (as gt command does)
	session.InitRegistry(townRoot)
	sock := tmux.GetDefaultSocket()
	fmt.Printf("Default Socket: %s\n", sock)

	cmd := exec.Command("tmux", "-L", sock, "list-sessions", "-F", "#{session_name}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Error: %v\nOutput: %s\n", err, string(out))
		return
	}
	fmt.Printf("Sessions:\n%s\n", string(out))
}
