//go:build !windows

package transport

import (
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/narixius/masir-go/libs/masir-core/paths"
)

func DialContext(ctx context.Context, endpoint string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", endpoint)
}

// Alive reports whether something accepts connections on endpoint.
func Alive(endpoint string) bool {
	conn, err := net.DialTimeout("unix", endpoint, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Listen serves endpoint to one user, whom the kernel's peer credentials identify:
// only uid and root may connect.
//
// A daemon running as that user keeps the socket in the user's directory, mode 0600. A
// root daemon keeps it in a directory only root can write (paths.PrivilegedRunDir),
// mode 0666 so the user can reach it: the peer check is what keeps other users out, and
// root never chowns a path the user could have swapped for a symlink.
func Listen(endpoint string, peer Peer) (net.Listener, error) {
	uid := peer.UID
	root := os.Geteuid() == 0
	if root {
		if err := paths.EnsureRootDir(filepath.Dir(endpoint), 0, 0o755); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", endpoint)
	if err != nil {
		if Alive(endpoint) {
			return nil, err
		}
		if os.Remove(endpoint) != nil { // a stale socket from a daemon that died
			return nil, err
		}
		if l, err = net.Listen("unix", endpoint); err != nil {
			return nil, err
		}
	}
	mode := os.FileMode(0o600)
	if root {
		mode = 0o666
	}
	if err := os.Chmod(endpoint, mode); err != nil {
		l.Close()
		return nil, err
	}
	return &peerChecked{Listener: l, allow: func(peer int) bool { return peer == uid || peer == 0 }}, nil
}

type peerChecked struct {
	net.Listener
	allow func(uid int) bool
}

// Accept drops connections from other users instead of returning an error, which
// would stop the gRPC server.
func (l *peerChecked) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err == nil && l.allow(uid) {
			return conn, nil
		}
		log.Printf("rejected connection from uid %d: %v", uid, err)
		conn.Close()
	}
}
