//go:build !windows

package updater

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ensureExited terminates the previous app if it is still alive after grace.
func ensureExited(pid int, wantImage string, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if p.Signal(syscall.Signal(0)) != nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return p.Kill()
}

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
