//go:build !darwin && !linux && !windows

package transport

import (
	"errors"
	"net"
)

// No peer-credential check is wired up here, so connections are refused rather than trusted.
func peerUID(net.Conn) (int, error) {
	return -1, errors.New("peer credentials aren't supported on this OS")
}
