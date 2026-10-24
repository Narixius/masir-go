//go:build windows

package transport

import (
	"context"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func DialContext(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}

// Alive reports whether something accepts connections on endpoint.
func Alive(endpoint string) bool {
	timeout := 500 * time.Millisecond
	conn, err := winio.DialPipe(endpoint, &timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Listen ignores uid and gid: Windows has no peer-credential check wired up yet,
// so access is whatever the pipe's default security allows.
func Listen(endpoint string, _, _ int) (net.Listener, error) {
	return winio.ListenPipe(endpoint, nil)
}
