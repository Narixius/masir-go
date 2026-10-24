package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/transport"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

var cachedClient pb.DaemonServiceClient

// daemon returns a client, starting a background daemon first if none answers.
func daemon() (pb.DaemonServiceClient, error) {
	if cachedClient != nil {
		return cachedClient, nil
	}
	sock, err := paths.SocketPath()
	if err != nil {
		return nil, err
	}
	if err := ensureDaemon(sock); err != nil {
		return nil, err
	}
	return dial(sock)
}

// runningDaemon returns a client only if a daemon already answers; it never starts one.
func runningDaemon() (pb.DaemonServiceClient, bool) {
	sock, err := paths.SocketPath()
	if err != nil || !transport.Alive(sock) {
		return nil, false
	}
	c, err := dial(sock)
	return c, err == nil
}

func dial(sock string) (pb.DaemonServiceClient, error) {
	conn, err := grpc.NewClient("passthrough:///masir-daemon",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return transport.DialContext(ctx, sock)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	cachedClient = pb.NewDaemonServiceClient(conn) // the process ends soon after; nothing to close
	return cachedClient, nil
}

// appCtx ends when the user quits: Ctrl-C, SIGTERM, or the terminal closing. Every
// daemon call and latency probe derives from it, so quitting cancels them all.
var appCtx = context.Background()

// rpc is a context for one daemon call. Fetching a subscription can take a while.
func rpc() (context.Context, context.CancelFunc) {
	return context.WithTimeout(appCtx, 90*time.Second)
}

// cancelled reports whether err means the user quit, rather than a failure.
func cancelled(err error) bool {
	if ui.IsCancel(err) || errors.Is(err, context.Canceled) {
		return true
	}
	return status.Code(err) == codes.Canceled || appCtx.Err() != nil
}

// rpcTimeoutShort bounds calls a person is waiting on interactively, like completions.
const rpcTimeoutShort = 3 * time.Second

// call runs one daemon call with a fresh context.
func call[T any](fn func(ctx context.Context, c pb.DaemonServiceClient) (T, error)) (T, error) {
	var zero T
	c, err := daemon()
	if err != nil {
		return zero, err
	}
	ctx, cancel := rpc()
	defer cancel()
	return fn(ctx, c)
}

// errorReason is the masir ErrorInfo reason on a status error, e.g. "ROOT_REQUIRED".
func errorReason(err error) string {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == "masir" {
			return info.Reason
		}
	}
	return ""
}

// message turns an error into what the user reads: the daemon's message without the
// gRPC wrapping, or what to do when the daemon can't be reached.
func message(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return err.Error()
	}
	// The daemon itself answers Unavailable when a subscription provider can't be
	// reached; only a failed connection to the daemon means the daemon is gone.
	if st.Code() == codes.Unavailable && strings.Contains(st.Message(), "connection error") {
		return "can't reach the masir daemon; start it with `masir daemon start`"
	}
	msg := st.Message()
	if hint := explain(msg); hint != "" {
		return msg + "\n" + hint
	}
	return msg
}

var portRe = regexp.MustCompile(`:(\d{2,5}): bind: address already in use`)

// explain turns errors that come from the system into what to do about them.
func explain(msg string) string {
	switch {
	case strings.Contains(msg, "failed to add system route") && strings.Contains(msg, "file exists"):
		return "Another VPN or TUN app already routes your traffic. " +
			"Quit it, then run `masir connect`."
	case strings.Contains(msg, "address already in use"):
		if m := portRe.FindStringSubmatch(msg); m != nil {
			return fmt.Sprintf("Port %s is taken by another program. Quit it, or move masir with `masir settings set mixedPort <port>`.", m[1])
		}
		return "A port masir needs is taken by another program."
	case strings.Contains(msg, "operation not permitted") && strings.Contains(msg, "tun"):
		return "TUN needs the daemon to run as root: `masir daemon enable` sets that up."
	case strings.Contains(msg, "geosite") || strings.Contains(msg, "geoip.dat"):
		return "geosite:/geoip: rules need geoip.dat and geosite.dat next to masird."
	}
	return ""
}

// errReported means the command already told the user what went wrong.
var errReported = errors.New("reported")

func fail(format string, a ...any) error { return fmt.Errorf(format, a...) }
