//go:build windows

package ui

import (
	"time"

	"golang.org/x/sys/windows"
)

// readable waits up to timeout for console input on fd.
func readable(fd int, timeout time.Duration) bool {
	ev, err := windows.WaitForSingleObject(windows.Handle(fd), uint32(timeout.Milliseconds()))
	return err == nil && ev == windows.WAIT_OBJECT_0
}
