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

// Listen opens the daemon's pipe for LocalSystem, Administrators and the served user
// only: on a shared machine, nobody else may control the daemon, which may run with
// Administrator rights.
func Listen(endpoint string, peer Peer) (net.Listener, error) {
	sddl, err := pipeSDDL(peer.SID)
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: sddl})
}
