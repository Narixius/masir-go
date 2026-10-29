// Package client is how a program reaches the masir daemon: finding the socket it
// answers on, dialing it, and starting a background daemon when none answers. The CLI
// and the desktop app share it.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/transport"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// Where daemons may answer, and where one starts. Tests point them elsewhere.
var (
	sockets    = paths.Sockets
	userSocket = paths.SocketPath
)

// LiveSocket is where a daemon answers right now: a root daemon's socket if there is
// one, otherwise the socket of a daemon running as this user. Either way the caller
// needs no privileges to reach it.
func LiveSocket() (string, bool) {
	socks, err := sockets()
	if err != nil {
		return "", false
	}
	for _, s := range socks {
		if transport.Alive(s) {
			return s, true
		}
	}
	return "", false
}

// Alive reports whether any daemon answers.
func Alive() bool {
	_, ok := LiveSocket()
	return ok
}

// AliveAt reports whether a daemon answers on sock.
func AliveAt(sock string) bool { return transport.Alive(sock) }

// DialSocket connects to the daemon on sock, for a caller that knows which daemon it
// means: a root process replacing the service that serves a uid, say. Close the
// connection when done.
func DialSocket(sock string) (*grpc.ClientConn, pb.DaemonServiceClient, error) {
	conn, err := grpc.NewClient("passthrough:///masir-daemon",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return transport.DialContext(ctx, sock)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return conn, pb.NewDaemonServiceClient(conn), nil
}

// Dial connects to whichever daemon answers. The socket is looked up again on every
// (re)connect: a root daemon taking over from this user's daemon listens somewhere else.
// Close the returned connection when done with it.
func Dial() (*grpc.ClientConn, pb.DaemonServiceClient, error) {
	userSock, err := userSocket()
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient("passthrough:///masir-daemon",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			sock, ok := LiveSocket()
			if !ok {
				sock = userSock
			}
			return transport.DialContext(ctx, sock)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return conn, pb.NewDaemonServiceClient(conn), nil
}

// ErrorReason is the masir ErrorInfo reason on a status error, e.g. "ROOT_REQUIRED".
func ErrorReason(err error) string {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == "masir" {
			return info.Reason
		}
	}
	return ""
}

// Start runs bin as a background daemon, as this user, unless something already answers
// (a background daemon or the root service). An idle daemon keeps Xray down until it is
// turned on. The daemon's output goes to masird.log in the config directory.
func Start(ctx context.Context, bin string, idle bool) error {
	if Alive() {
		return nil
	}
	sock, err := userSocket() // where a daemon starts, if none answers
	if err != nil {
		return err
	}
	dir, err := paths.ConfigDir()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "masird.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	args := []string{"daemon", "run"}
	if idle {
		args = append(args, "--idle")
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the masir daemon: %w", err)
	}
	_ = cmd.Process.Release()
	return WaitFor(ctx, func() bool { return transport.Alive(sock) }, 10*time.Second,
		"the masir daemon didn't start; see "+logf.Name())
}

// WaitFor polls ok until it holds, ctx ends, or timeout passes, in which case it fails
// with the failure text.
func WaitFor(ctx context.Context, ok func() bool, timeout time.Duration, failure string) error {
	deadline := time.Now().Add(timeout)
	for !ok() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New(failure)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}
