//go:build !windows

package transport

import (
	"context"
	"log"
	"net"
	"os"
	"time"
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

// Listen serves endpoint to one user: the socket belongs to uid with mode 0600, and
// only uid and root may connect, checked by the kernel's peer credentials.
func Listen(endpoint string, peer Peer) (net.Listener, error) {
	uid, gid := peer.UID, peer.GID
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
	if os.Geteuid() == 0 {
		if err := os.Chown(endpoint, uid, gid); err != nil {
			l.Close()
			return nil, err
		}
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
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
