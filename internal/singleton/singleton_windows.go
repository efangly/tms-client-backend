//go:build windows

// Package singleton keeps a single tms-backend instance per machine.
package singleton

import (
	"time"

	"golang.org/x/sys/windows"
)

// Acquire takes a machine-wide named mutex, waiting up to wait for a previous
// holder (e.g. the old version during an update) to exit. It returns false if
// another instance still holds it. The mutex is held until the process exits.
// If the mutex cannot be created at all it fails open (returns true).
func Acquire(name string, wait time.Duration) bool {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return true
	}
	deadline := time.Now().Add(wait)
	for {
		h, err := windows.CreateMutex(nil, false, namePtr)
		if err == nil {
			return true // handle intentionally kept open for the process lifetime
		}
		if h == 0 || err != windows.ERROR_ALREADY_EXISTS {
			return true
		}
		windows.CloseHandle(h)
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}
