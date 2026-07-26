//go:build !windows

package daemon

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func startZombieReaper(logger *log.Logger) {
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGCHLD)
		for range ch {
			for {
				var status syscall.WaitStatus
				pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
				if pid <= 0 {
					break
				}
				_ = err
			}
		}
	}()
}
