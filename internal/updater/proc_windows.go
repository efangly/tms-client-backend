package updater

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detach makes cmd outlive its parent, which exits right after starting it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup,
		HideWindow:    true,
	}
}

// ensureExited waits up to grace for the process pid (the previous app, now
// running from exe.old) to exit, then terminates it if it is still alive.
// A leftover old instance would keep holding serial ports and the HTTP port
// and block the new version. Only a process whose image is wantImage is
// touched, so a recycled PID is never killed.
func ensureExited(pid int, wantImage string, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil // already gone
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH*2)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err == nil {
		img := windows.UTF16ToString(buf[:size])
		if !strings.EqualFold(filepath.Base(img), filepath.Base(wantImage)) {
			return nil // PID was recycled by an unrelated process
		}
	}

	ev, _ := windows.WaitForSingleObject(h, uint32(grace.Milliseconds()))
	if ev == windows.WAIT_OBJECT_0 {
		return nil
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	windows.WaitForSingleObject(h, 5000)
	return nil
}
