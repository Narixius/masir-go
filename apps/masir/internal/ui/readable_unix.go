//go:build !windows

package ui

import (
	"time"

	"golang.org/x/sys/unix"
)

// readable waits up to timeout for input on fd. Polling instead of a blocked read
// means no goroutine is left reading after the picker closes, to steal the first
// key from whatever prompt comes next.
func readable(fd int, timeout time.Duration) bool {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, int(timeout.Milliseconds()))
	return err == nil && n > 0
}
