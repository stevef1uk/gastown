package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	townRoot := "/home/stevef/gt"
	sessionID := "hq-qsq-furiosa"
	pidFile := filepath.Join(townRoot, ".gt-nats-pids", sessionID)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Printf("Error reading PID file: %v\n", err)
		return
	}
	pidStr := strings.TrimSpace(string(data))
	fmt.Printf("PID from file: %q\n", pidStr)

	cmd := exec.Command("ps", "-p", pidStr, "-o", "pid=")
	err = cmd.Run()
	fmt.Printf("ps -p %s exit status: %v\n", pidStr, err)
}
