//go:build !windows

package singleton

import "time"

// Acquire is a no-op outside Windows.
func Acquire(name string, wait time.Duration) bool { return true }
